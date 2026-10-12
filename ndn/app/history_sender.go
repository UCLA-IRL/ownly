//go:build js && wasm

package app

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/UCLA-IRL/ownly/ndn/historylog"
	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/log"
	merkledef "github.com/named-data/ndnd/std/ndn/merklelog"
)

const (
	historyRetryInitial = time.Second
	historyRetryMax     = time.Minute
)

type historyAppender interface {
	Append([][]byte, func(*merkledef.AppendResponse, error))
}

// historySender turns durable rows for one identity/log scope into append
// commands. The outbox remains authoritative; the batcher is only a bounded
// in-memory scheduling window.
type historySender struct {
	scope    historylog.Scope
	outbox   historylog.Outbox
	appender historyAppender
	batcher  *historylog.Batcher

	mu         sync.Mutex
	rows       map[historylog.Hash]historylog.Pending
	pumping    bool
	pumpAgain  bool
	retryTimer *time.Timer
	retryDelay time.Duration
	closed     bool
}

func newHistorySender(scope historylog.Scope, outbox historylog.Outbox, appender historyAppender) (*historySender, error) {
	if outbox == nil || appender == nil {
		return nil, fmt.Errorf("history sender requires an outbox and append client")
	}
	sender := &historySender{
		scope:      scope,
		outbox:     outbox,
		appender:   appender,
		rows:       make(map[historylog.Hash]historylog.Pending),
		retryDelay: historyRetryInitial,
	}
	batcher, err := historylog.NewBatcher(historylog.Options{}, sender.submit, sender.retry)
	if err != nil {
		return nil, err
	}
	sender.batcher = batcher
	return sender, nil
}

// Wake discovers at most one durable page. A later acknowledgement or commit
// wakes the next page, keeping the batcher's working set bounded.
func (s *historySender) Wake() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.pumping {
		s.pumpAgain = true
		s.mu.Unlock()
		return
	}
	s.pumping = true
	s.mu.Unlock()
	go s.pump()
}

func (s *historySender) pump() {
	for {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return
		}
		rows, err := s.outbox.Pending(s.scope, 0, historylog.PendingPageSize)
		if err != nil {
			s.retry(fmt.Errorf("read history outbox: %w", err))
		} else {
			for _, row := range rows {
				s.mu.Lock()
				_, loaded := s.rows[row.Hash]
				if loaded {
					s.mu.Unlock()
					continue
				}
				s.rows[row.Hash] = row
				s.mu.Unlock()
				if err := s.batcher.Enqueue(row.Hash); err != nil {
					s.mu.Lock()
					if current, ok := s.rows[row.Hash]; ok && current == row {
						delete(s.rows, row.Hash)
					}
					s.mu.Unlock()
					if errors.Is(err, historylog.ErrClosed) {
						return
					}
					if !errors.Is(err, historylog.ErrCapacity) {
						s.retry(fmt.Errorf("queue history row: %w", err))
					}
					break
				}
			}
		}

		s.mu.Lock()
		if !s.pumpAgain {
			s.pumping = false
			s.mu.Unlock()
			return
		}
		s.pumpAgain = false
		s.mu.Unlock()
	}
}

func (s *historySender) submit(hashes []historylog.Hash, complete func([]bool, error)) {
	rows := make([]historylog.Pending, len(hashes))
	dataHashes := make([][]byte, len(hashes))
	s.mu.Lock()
	for i, hash := range hashes {
		row, ok := s.rows[hash]
		if !ok {
			s.mu.Unlock()
			complete(nil, fmt.Errorf("history row disappeared before append"))
			return
		}
		rows[i] = row
		dataHashes[i] = hash[:]
	}
	s.mu.Unlock()

	s.appender.Append(dataHashes, func(response *merkledef.AppendResponse, err error) {
		if err != nil {
			complete(nil, err)
			return
		}

		acknowledged := make([]bool, len(rows))
		ackRows := make([]historylog.Pending, 0, len(rows))
		for i, result := range response.Results {
			acknowledged[i] = result.Status == merkledef.AppendStatusOK || result.Status == merkledef.AppendStatusDuplicate
			if acknowledged[i] {
				ackRows = append(ackRows, rows[i])
			}
		}
		if len(ackRows) > 0 {
			if err = s.outbox.Ack(s.scope, ackRows); err != nil {
				complete(nil, fmt.Errorf("dequeue history acknowledgement: %w", err))
				return
			}
		}

		allAcknowledged := len(ackRows) == len(rows)
		if allAcknowledged {
			s.resetRetry()
		}
		complete(acknowledged, nil)
		if len(ackRows) > 0 {
			s.mu.Lock()
			for i, row := range rows {
				if current, ok := s.rows[hashes[i]]; acknowledged[i] && ok && current == row {
					delete(s.rows, hashes[i])
				}
			}
			s.mu.Unlock()
		}
		s.Wake()
	})
}

func (s *historySender) retry(err error) {
	log.Warn(nil, "History append will retry", "err", err)
	s.mu.Lock()
	if s.closed || s.retryTimer != nil {
		s.mu.Unlock()
		return
	}
	delay := s.retryDelay
	s.retryDelay = min(s.retryDelay*2, historyRetryMax)
	s.retryTimer = time.AfterFunc(delay, func() {
		s.mu.Lock()
		s.retryTimer = nil
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return
		}
		if err := s.batcher.Flush(); err != nil {
			s.retry(err)
			return
		}
		s.Wake()
	})
	s.mu.Unlock()
}

func (s *historySender) resetRetry() {
	s.mu.Lock()
	if s.retryTimer != nil {
		s.retryTimer.Stop()
		s.retryTimer = nil
	}
	s.retryDelay = historyRetryInitial
	s.mu.Unlock()
}

func (s *historySender) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	if s.retryTimer != nil {
		s.retryTimer.Stop()
		s.retryTimer = nil
	}
	s.mu.Unlock()
	s.batcher.Close()
}

func (a *App) ensureHistorySender(identity enc.Name, scope historylog.Scope) error {
	if a.engine == nil || a.historyOutbox == nil {
		return nil
	}
	key := scope.Key()
	a.historyMu.Lock()
	if sender := a.historySenders[key]; sender != nil {
		a.historyMu.Unlock()
		sender.Wake()
		return nil
	}
	a.historyMu.Unlock()

	client, err := a.historyLogClient(identity)
	if err != nil {
		return err
	}
	sender, err := newHistorySender(scope, a.historyOutbox, client)
	if err != nil {
		return err
	}
	a.historyMu.Lock()
	if existing := a.historySenders[key]; existing != nil {
		a.historyMu.Unlock()
		sender.Close()
		return nil
	}
	a.historySenders[key] = sender
	a.historyMu.Unlock()
	sender.Wake()
	return nil
}

func (a *App) wakeHistorySender(scope historylog.Scope) {
	a.historyMu.Lock()
	sender := a.historySenders[scope.Key()]
	a.historyMu.Unlock()
	if sender != nil {
		sender.Wake()
	}
}

func (a *App) resumeHistorySenders() {
	if a.historyLogID == "" || a.historyOutbox == nil {
		return
	}
	for _, identity := range a.keychain.Identities() {
		owned := false
		for _, key := range identity.Keys() {
			if key.Signer() != nil {
				owned = true
				break
			}
		}
		if !owned {
			continue
		}
		scope, err := historylog.NewScope(identity.Name(), a.historyLogID)
		if err != nil {
			log.Warn(a, "Cannot resume history sender", "identity", identity.Name(), "err", err)
			continue
		}
		if err = a.ensureHistorySender(identity.Name(), scope); err != nil {
			log.Warn(a, "Cannot start history sender", "identity", identity.Name(), "err", err)
		}
	}
}
