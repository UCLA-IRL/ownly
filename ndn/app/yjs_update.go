//go:build js && wasm

package app

import (
	"syscall/js"

	"github.com/UCLA-IRL/ownly/ndn/app/tlv"
	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/ndn/svs_ps"
	jsutil "github.com/named-data/ndnd/std/utils/js"
)

var _yjs_merge_updates = js.Global().Get("_yjs_merge_updates")

// CompressSnapshotYjs compresses Yjs updates in the history snapshot.
// This follows the SvsALO rules for snapshot compression.
func (a *App) CompressSnapshotYjs(hs *svs_ps.HistorySnap) {
	// Compress Yjs updates in the snapshot.
	// But we need to compress documents individually.
	updateMap := make(map[string][][]byte)
	lastEntry := make(map[string]*svs_ps.HistorySnapEntry) // see rules for snapshot compress

	for _, entry := range hs.Entries {
		// Parse entry to check if it is a Yjs update
		msg, err := tlv.ParseMessage(enc.NewWireView(entry.Content), true)
		if err != nil {
			log.Error(nil, "Failed to parse snapshot entry", "err", err)
			continue
		}

		// Application updates are encrypted, decrypt it again
		msg, err = a.decryptPub(msg)
		if err != nil {
			log.Error(nil, "Failed to decrypt snapshot entry", "err", err)
			continue
		}

		if msg.YjsDelta != nil {
			updateMap[msg.YjsDelta.UUID] = append(updateMap[msg.YjsDelta.UUID], msg.YjsDelta.Binary)
			lastEntry[msg.YjsDelta.UUID] = entry
			entry.Content = nil // remove this entry later if this is still nil
		}
	}

	// Compress Yjs updates
	for uuid, updates := range updateMap {
		updatesJs := js.Global().Get("Array").New()
		for _, update := range updates {
			updatesJs.Call("push", jsutil.SliceToJsArray(update))
		}
		merged := jsutil.JsArrayToSlice(_yjs_merge_updates.Invoke(updatesJs))

		// Create new message
		msg := &tlv.Message{
			YjsDelta: &tlv.YjsDelta{
				UUID:   uuid,
				Binary: merged,
			},
		}

		// Encrypt the snapshot entry
		msg, err := a.encryptPub(msg, lastEntry[uuid].SeqNo)
		if err != nil {
			log.Error(nil, "Failed to encrypt snapshot entry", "err", err)
			continue
		}

		lastEntry[uuid].Content = msg.Encode()
	}

	// Remove all entries with nil content
	finalEntries := make([]*svs_ps.HistorySnapEntry, 0, len(hs.Entries))
	for _, entry := range hs.Entries {
		if entry.Content != nil {
			finalEntries = append(finalEntries, entry)
		}
	}
	hs.Entries = finalEntries
}
