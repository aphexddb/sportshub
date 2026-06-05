package app

import (
	"sync"

	"sportshub2/internal/status"
)

// bootState tracks the app's startup sequence so a single loading spinner with a live status
// message can be driven over SSE until the server is ready. It is concurrency-safe: boot runs
// on its own goroutine while HTTP (and SSE snapshot reads) are already serving.
type bootState struct {
	mu     sync.Mutex
	order  []string // check names, in display order
	checks map[string]*status.InitCheck
	phase  string
	done   bool
	errMsg string
}

// newBootState pre-declares the ordered checks, all pending.
func newBootState(names ...string) *bootState {
	b := &bootState{checks: make(map[string]*status.InitCheck), phase: "Starting…"}
	for _, n := range names {
		b.order = append(b.order, n)
		b.checks[n] = &status.InitCheck{Name: n, Status: "pending"}
	}
	return b
}

// start marks a check running and sets the current spinner phase message.
func (b *bootState) start(name, phase string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.phase = phase
	if c := b.checks[name]; c != nil {
		c.Status = "running"
	}
}

// ok marks a check successful.
func (b *bootState) ok(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c := b.checks[name]; c != nil {
		c.Status = "ok"
	}
}

// fail marks a check errored and records the overall boot error (non-fatal steps may continue).
func (b *bootState) fail(name, detail string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c := b.checks[name]; c != nil {
		c.Status = "error"
		c.Detail = detail
	}
	b.errMsg = detail
}

// finish completes the boot sequence. On success it marks Done (the spinner hides); if any
// step failed it leaves Done false so the overlay stays up showing the error — a failed media
// server / missing ffmpeg means the app can't stream, so revealing it would be misleading.
func (b *bootState) finish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.errMsg == "" {
		b.done = true
		b.phase = "Ready"
	} else {
		b.done = false
		b.phase = "Startup failed"
	}
}

// snapshot returns the current init state for inclusion in the SSE Snapshot.
func (b *bootState) snapshot() status.InitState {
	b.mu.Lock()
	defer b.mu.Unlock()
	checks := make([]status.InitCheck, 0, len(b.order))
	for _, n := range b.order {
		if c := b.checks[n]; c != nil {
			checks = append(checks, *c)
		}
	}
	return status.InitState{Phase: b.phase, Done: b.done, Error: b.errMsg, Checks: checks}
}
