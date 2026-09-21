//go:build js && wasm

package historylog

import (
	"encoding/hex"
	"fmt"
	"syscall/js"

	jsutil "github.com/named-data/ndnd/std/utils/js"
)

// JSOutbox awaits every database operation. Call it from a goroutine, not a
// synchronous js.FuncOf callback, so the browser can resolve the promises.
type JSOutbox struct {
	api         js.Value
	afterCommit func(Scope)
}

func NewJSOutbox(api js.Value, afterCommit func(Scope)) *JSOutbox {
	return &JSOutbox{api: api, afterCommit: afterCommit}
}

func (s *JSOutbox) CommitProduced(scope Scope, packets []Packet) error {
	entries := make([]any, len(packets))
	for i, packet := range packets {
		entries[i] = map[string]any{
			"name": jsutil.SliceToJsArray(packet.name.BytesInner()),
			"blob": jsutil.SliceToJsArray(packet.wire),
			"hash": hex.EncodeToString(packet.hash[:]),
		}
	}
	if _, err := jsutil.Await(s.api.Call("history_commit", scope.key, entries)); err != nil {
		return fmt.Errorf("history commit: %w", err)
	}
	if len(packets) > 0 && s.afterCommit != nil {
		s.afterCommit(scope)
	}
	return nil
}

func (s *JSOutbox) Pending(scope Scope, after uint64, limit int) ([]Pending, error) {
	rows, err := jsutil.Await(s.api.Call("history_pending", scope.key, float64(after), limit))
	if err != nil {
		return nil, fmt.Errorf("history pending: %w", err)
	}
	count := rows.Length()
	result := make([]Pending, 0, count)
	for i := 0; i < count; i++ {
		row := rows.Index(i)
		id := row.Get("id").Float()
		hash, err := hex.DecodeString(row.Get("hash").String())
		if err != nil || len(hash) != len(Hash{}) {
			return nil, fmt.Errorf("invalid stored history digest")
		}
		result = append(result, Pending{ID: uint64(id), Hash: Hash(hash)})
	}
	return result, nil
}

func (s *JSOutbox) Ack(scope Scope, acknowledged []Pending) error {
	rows := make([]any, len(acknowledged))
	for i, row := range acknowledged {
		rows[i] = map[string]any{"id": float64(row.ID), "hash": hex.EncodeToString(row.Hash[:])}
	}
	if _, err := jsutil.Await(s.api.Call("history_ack", scope.key, rows)); err != nil {
		return fmt.Errorf("history dequeue: %w", err)
	}
	return nil
}
