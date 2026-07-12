package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// watchEvent is one Server-Sent Event payload. The five fields the M3 contract
// names (issuance_id, status, confirmations, anchor_depth, txid) are always
// present; the rest are additive context a truthful chip renders ("anchored at
// Bitcoin block H", the explorer deep link, the reorg banner).
type watchEvent struct {
	IssuanceID    string `json:"issuance_id"`
	Status        string `json:"status"`
	Confirmations int64  `json:"confirmations"`
	AnchorDepth   int64  `json:"anchor_depth"`
	Txid          string `json:"txid"`
	AssetID       string `json:"asset_id,omitempty"`
	AnchorHeight  int64  `json:"anchor_height,omitempty"`
	AnchorHash    string `json:"anchor_hash,omitempty"`
	AnchorStatus  string `json:"anchor_status,omitempty"`
	BlockHeight   int64  `json:"block_height,omitempty"`
	RegistryURL   string `json:"registry_url,omitempty"`
	TS            int64  `json:"ts"`
}

func eventFrom(w *WatchState) watchEvent {
	return watchEvent{
		IssuanceID:    w.IssuanceID,
		Status:        w.Status,
		Confirmations: w.Confirmations,
		AnchorDepth:   w.AnchorDepth,
		Txid:          w.Txid,
		AssetID:       w.AssetID,
		AnchorHeight:  w.AnchorHeight,
		AnchorHash:    w.AnchorHash,
		AnchorStatus:  w.AnchorStatus,
		BlockHeight:   w.BlockHeight,
		RegistryURL:   w.RegistryURL,
		TS:            time.Now().Unix(),
	}
}

// sseBroker fans watch-state changes out to connected SSE subscribers. Each
// subscriber is scoped to one principal's AID: it only ever receives events for
// issuances that AID owns, so a session can never observe another account's
// chain state. Subscribers are a simple per-connection channel; a slow or gone
// client is dropped rather than blocking the watcher.
type sseBroker struct {
	mu   sync.Mutex
	next int
	subs map[int]*sseSub
}

type sseSub struct {
	aid string
	ch  chan watchEvent
}

func newSSEBroker() *sseBroker { return &sseBroker{subs: map[int]*sseSub{}} }

func (b *sseBroker) subscribe(aid string) (int, chan watchEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	ch := make(chan watchEvent, 16)
	b.subs[id] = &sseSub{aid: aid, ch: ch}
	return id, ch
}

func (b *sseBroker) unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if sub, ok := b.subs[id]; ok {
		delete(b.subs, id)
		close(sub.ch)
	}
}

// publish delivers an event to every subscriber whose AID owns it. A full
// channel (a wedged client) is skipped rather than blocked: the watcher's
// persisted state remains the source of truth, so a dropped event is recovered
// on the next change or the next connect snapshot.
func (b *sseBroker) publish(ownerAID string, ev watchEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sub := range b.subs {
		if sub.aid != ownerAID {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
		}
	}
}

// bus lazily builds the broker so a server constructed directly in a test (which
// never starts the watcher) still serves SSE without extra wiring.
func (s *server) bus() *sseBroker {
	s.busOnce.Do(func() { s.sse = newSSEBroker() })
	return s.sse
}

// handleEvents is GET /api/events: a session-scoped Server-Sent Events stream.
// On connect it replays the current state of every issuance the principal owns
// (so a late joiner is never blank), then streams changes until the client
// disconnects. A comment heartbeat keeps the connection and any intermediary
// from idling it out.
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)

	id, ch := s.bus().subscribe(acct.AID)
	defer s.bus().unsubscribe(id)

	// Snapshot: the principal's whole watched world, current as of now.
	if snap, err := s.st.WatchByOwner(acct.AID); err == nil {
		for _, wst := range snap {
			writeSSE(w, eventFrom(wst))
		}
	}
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			writeSSE(w, ev)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, ev watchEvent) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: watch\ndata: %s\n\n", b)
}
