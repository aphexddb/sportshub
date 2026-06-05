package sse

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSubscribeReceivesBroadcast(t *testing.T) {
	h := New()
	ch := h.Subscribe()

	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	want := payload{Name: "hello", Count: 7}
	h.Broadcast(want)

	select {
	case msg := <-ch:
		if !strings.HasPrefix(msg, "data: ") {
			t.Fatalf("message missing %q prefix: %q", "data: ", msg)
		}
		if !strings.HasSuffix(msg, "\n\n") {
			t.Fatalf("message missing trailing newlines: %q", msg)
		}
		line := strings.TrimSuffix(strings.TrimPrefix(msg, "data: "), "\n\n")
		var got payload
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("unmarshal SSE data: %v (line=%q)", err, line)
		}
		if got != want {
			t.Fatalf("round-trip mismatch: got %+v want %+v", got, want)
		}
	default:
		t.Fatal("expected a message on the subscriber channel, got none")
	}
}

func TestBroadcastDoesNotBlockOnFullClient(t *testing.T) {
	h := New()
	h.Subscribe() // never drained; buffer size is 8

	// Broadcast many more than the buffer can hold. Each call must return
	// promptly even though the subscriber is "slow" (its buffer fills and
	// further sends are dropped).
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			h.Broadcast(i)
		}
		close(done)
	}()

	select {
	case <-done:
		// success: Broadcast did not block
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked on a full/slow client")
	}
}

func TestUnsubscribeClosesAndRemoves(t *testing.T) {
	h := New()
	ch := h.Subscribe()
	h.Unsubscribe(ch)

	// Channel must be closed.
	if _, ok := <-ch; ok {
		t.Fatal("expected channel to be closed after Unsubscribe")
	}

	// A subsequent Broadcast must not panic (would happen if we still sent
	// on the now-closed channel) and must not deliver to the removed sub.
	h.Broadcast("after-unsub")
}

func TestBroadcastSkipsUnmarshalableValue(t *testing.T) {
	h := New()
	ch := h.Subscribe()

	// Channels cannot be JSON-marshaled; Broadcast should silently no-op.
	h.Broadcast(make(chan int))

	select {
	case msg := <-ch:
		t.Fatalf("expected no message for unmarshalable value, got %q", msg)
	default:
		// success
	}
}
