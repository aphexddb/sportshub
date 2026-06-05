// Package sse provides a generic Server-Sent-Events hub that fans out
// JSON-encoded messages to any number of subscribed clients.
package sse

import (
	"encoding/json"
	"sync"
)

// Hub is a concurrency-safe fan-out for Server-Sent-Events. Each subscriber
// receives a buffered channel of pre-formatted SSE messages. Slow subscribers
// (whose buffers are full) are skipped rather than blocking the broadcaster.
type Hub struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

// New returns a ready-to-use Hub.
func New() *Hub {
	return &Hub{subs: make(map[chan string]struct{})}
}

// Subscribe registers a new subscriber and returns its channel. The channel is
// buffered (size 8); callers should range over it to receive SSE messages and
// must call Unsubscribe when done.
func (h *Hub) Subscribe() chan string {
	ch := make(chan string, 8)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe removes ch from the hub and closes it.
func (h *Hub) Unsubscribe(ch chan string) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
	close(ch)
}

// Broadcast JSON-marshals v, formats it as an SSE "data:" frame, and delivers
// it to every subscriber without blocking. Subscribers whose buffers are full
// are dropped for this message. If v cannot be marshaled, Broadcast is a no-op.
func (h *Hub) Broadcast(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	msg := "data: " + string(b) + "\n\n"
	h.mu.Lock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default:
			// drop on slow client
		}
	}
	h.mu.Unlock()
}
