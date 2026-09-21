// Package historylog provides producer-side scheduling for history log submissions.
package historylog

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Hash identifies one exact, complete signed Data packet.
type Hash [sha256.Size]byte

var (
	ErrClosed   = errors.New("history batcher is closed")
	ErrCapacity = errors.New("history batcher is full")
)

// Options bounds scheduling and memory use. Zero fields use the defaults below.
// MaxBatchHashes limits hash count, not the encoded NDN packet size.
type Options struct {
	IdleDelay        time.Duration
	MaxBatchAge      time.Duration
	MaxBatchHashes   int
	MaxPendingHashes int
}

// SubmitFunc must return promptly. It may complete synchronously or asynchronously.
// The completion bitmap corresponds to hashes in request order. A request error
// retains the whole batch; otherwise only acknowledged hashes are removed.
// A durable adapter must commit dequeue of acknowledged entries before completing.
type SubmitFunc func(hashes []Hash, complete func(acknowledged []bool, err error))

type queuedHash struct {
	hash Hash
	at   time.Time
}

type submission struct {
	entries []queuedHash
}

// Batcher holds unresolved hashes in memory. It does not provide persistence or
// automatic retries. Failed submissions pause sending until Flush is called.
// All public methods are safe for concurrent use.
type Batcher struct {
	mu          sync.Mutex
	opts        Options
	submit      SubmitFunc
	onError     func(error)
	pending     []queuedHash
	unresolved  map[Hash]struct{}
	inFlight    *submission
	lastArrival time.Time
	timer       *time.Timer
	timerID     uint64
	ready       bool
	paused      bool
	closed      bool
}

// NewBatcher creates an empty queue with a one-second idle delay, five-second
// maximum batch age, 64 hashes per batch, and 4096 unresolved hashes by default.
// submit and onError must be non-nil and must not block. Neither runs under the
// queue lock, so callbacks may call back into the batcher.
func NewBatcher(opts Options, submit SubmitFunc, onError func(error)) (*Batcher, error) {
	if opts.IdleDelay == 0 {
		opts.IdleDelay = time.Second
	}
	if opts.MaxBatchAge == 0 {
		opts.MaxBatchAge = 5 * time.Second
	}
	if opts.MaxBatchHashes == 0 {
		opts.MaxBatchHashes = 64
	}
	if opts.MaxPendingHashes == 0 {
		opts.MaxPendingHashes = 4096
	}
	if opts.IdleDelay < 0 || opts.MaxBatchAge < opts.IdleDelay {
		return nil, fmt.Errorf("invalid history batcher delays")
	}
	if opts.MaxBatchHashes < 1 || opts.MaxPendingHashes < opts.MaxBatchHashes {
		return nil, fmt.Errorf("invalid history batcher capacity")
	}
	if submit == nil || onError == nil {
		return nil, fmt.Errorf("history batcher requires submit and error callbacks")
	}
	return &Batcher{
		opts:       opts,
		submit:     submit,
		onError:    onError,
		unresolved: make(map[Hash]struct{}),
	}, nil
}

// Enqueue schedules a digest. Duplicate unresolved hashes do not move the timer.
// ErrCapacity leaves the new digest unqueued; a durable caller must retain it.
func (b *Batcher) Enqueue(hash Hash) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrClosed
	}
	if _, exists := b.unresolved[hash]; exists {
		b.mu.Unlock()
		return nil
	}
	if len(b.unresolved) >= b.opts.MaxPendingHashes {
		b.mu.Unlock()
		return ErrCapacity
	}
	b.lastArrival = time.Now()
	b.pending = append(b.pending, queuedHash{hash: hash, at: b.lastArrival})
	b.unresolved[hash] = struct{}{}
	next := b.scheduleLocked()
	b.mu.Unlock()
	b.dispatch(next)
	return nil
}

// Flush makes pending hashes ready immediately, and resumes a paused queue.
// It returns without waiting for acknowledgement. An in-flight request finishes
// before another starts; a subsequent failure requires another explicit Flush.
func (b *Batcher) Flush() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrClosed
	}
	b.paused = false
	b.ready = len(b.pending) > 0
	next := b.scheduleLocked()
	b.mu.Unlock()
	b.dispatch(next)
	return nil
}

// Close stops scheduling and returns unresolved hashes in arrival order.
// It is idempotent. It cannot cancel a submission already selected for dispatch;
// any later completion is ignored. Closing does not acknowledge or persist work.
func (b *Batcher) Close() []Hash {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	b.cancelTimerLocked()
	remaining := make([]Hash, 0, len(b.unresolved))
	if b.inFlight != nil {
		for _, entry := range b.inFlight.entries {
			remaining = append(remaining, entry.hash)
		}
	}
	for _, entry := range b.pending {
		remaining = append(remaining, entry.hash)
	}
	b.pending = nil
	b.inFlight = nil
	b.unresolved = nil
	return remaining
}

func (b *Batcher) cancelTimerLocked() {
	b.timerID++
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
}

// scheduleLocked either reserves one ready batch or arms the next deadline.
// A due batch waits for an available sender without resetting its age.
func (b *Batcher) scheduleLocked() *submission {
	b.cancelTimerLocked()
	if b.closed || b.paused || len(b.pending) == 0 {
		return nil
	}
	deadline := b.lastArrival.Add(b.opts.IdleDelay)
	if upper := b.pending[0].at.Add(b.opts.MaxBatchAge); upper.Before(deadline) {
		deadline = upper
	}
	if b.ready || len(b.pending) >= b.opts.MaxBatchHashes || !time.Now().Before(deadline) {
		// New arrivals must not postpone a window that became ready while the
		// sender was busy. Keep readiness until that pending window is drained.
		b.ready = true
		if b.inFlight != nil {
			return nil
		}
		n := min(len(b.pending), b.opts.MaxBatchHashes)
		next := &submission{entries: append([]queuedHash(nil), b.pending[:n]...)}
		clear(b.pending[:n])
		b.pending = b.pending[n:]
		if len(b.pending) == 0 {
			b.pending = nil
			b.ready = false
		}
		b.inFlight = next
		// With a request in flight this only arms the remaining queue's timer.
		b.scheduleLocked()
		return next
	}
	timerID := b.timerID
	b.timer = time.AfterFunc(time.Until(deadline), func() {
		b.mu.Lock()
		if timerID != b.timerID || b.closed {
			b.mu.Unlock()
			return
		}
		next := b.scheduleLocked()
		b.mu.Unlock()
		b.dispatch(next)
	})
	return nil
}

func (b *Batcher) dispatch(next *submission) {
	if next == nil {
		return
	}
	// Hash arrays are copied so the submitter cannot mutate retained work.
	hashes := make([]Hash, len(next.entries))
	for i, entry := range next.entries {
		hashes[i] = entry.hash
	}
	b.submit(hashes, func(acknowledged []bool, err error) {
		b.complete(next, acknowledged, err)
	})
}

func (b *Batcher) complete(request *submission, acknowledged []bool, err error) {
	b.mu.Lock()
	if b.closed || b.inFlight != request {
		b.mu.Unlock()
		return // Late or duplicate completion must not affect another batch.
	}
	if err == nil && len(acknowledged) != len(request.entries) {
		err = fmt.Errorf("history batcher: got %d acknowledgements for %d hashes", len(acknowledged), len(request.entries))
	}
	failed := make([]queuedHash, 0, len(request.entries))
	for i, entry := range request.entries {
		if err != nil || !acknowledged[i] {
			failed = append(failed, entry)
		} else {
			delete(b.unresolved, entry.hash)
		}
	}
	b.inFlight = nil
	if len(failed) > 0 {
		b.pending = append(failed, b.pending...)
		b.paused = true
		b.ready = false
		if err == nil {
			err = fmt.Errorf("history batcher: %d hashes were not acknowledged", len(failed))
		}
	}
	next := b.scheduleLocked()
	b.mu.Unlock()
	if err != nil {
		b.onError(err)
	}
	b.dispatch(next)
}
