package main

import (
	"sync"
	"time"

	"sportshub2/pkg/media"
)

// InitManager is the single source of truth for the app's boot lifecycle.
//
// The HTTP server is started first; the slow boot steps (port cleanup, binary
// download, MediaMTX startup) run on a background goroutine and report progress
// here. The SSE layer reads InitState() and pushes it to the frontend spinner
// every 500ms until Ready becomes true.
type InitManager struct {
	mu      sync.RWMutex
	phase   string // short machine-ish phase id (e.g. "cleanup", "binaries", "mediamtx", "ready", "error")
	message string // human-readable status shown in the spinner
	errMsg  string // non-empty if a boot step failed
	ready   bool
}

// InitState is an immutable snapshot of the boot lifecycle for the SSE payload.
type InitState struct {
	Ready   bool   `json:"initReady"`
	Phase   string `json:"initPhase"`
	Message string `json:"initMessage"`
	Error   string `json:"initError,omitempty"`
}

// initMgr is the process-wide boot state. It starts in the cleanup phase so the
// very first SSE snapshot (even before the boot goroutine runs) reports "not ready".
var initMgr = &InitManager{
	phase:   "starting",
	message: "Starting up…",
}

// SetPhase records the current boot phase + human message (not ready, no error).
func (m *InitManager) SetPhase(phase, message string) {
	m.mu.Lock()
	m.phase = phase
	m.message = message
	m.errMsg = ""
	m.mu.Unlock()
}

// Fail marks the boot as errored. The frontend keeps the overlay up and shows
// the message in red. Ready stays false.
func (m *InitManager) Fail(phase, message string) {
	m.mu.Lock()
	m.phase = phase
	m.errMsg = message
	if message != "" {
		m.message = message
	}
	m.ready = false
	m.mu.Unlock()
}

// MarkReady flips the boot to complete. The frontend reveals the app.
func (m *InitManager) MarkReady() {
	m.mu.Lock()
	m.phase = "ready"
	m.message = "Ready"
	m.errMsg = ""
	m.ready = true
	m.mu.Unlock()
}

// IsReady reports whether the boot sequence has completed successfully.
func (m *InitManager) IsReady() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ready
}

// State returns an immutable snapshot for embedding in the SSE payload.
func (m *InitManager) State() InitState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return InitState{
		Ready:   m.ready,
		Phase:   m.phase,
		Message: m.message,
		Error:   m.errMsg,
	}
}

// runBoot executes the slow startup steps in order, reporting progress to the
// InitManager between each one. It is meant to run on its own goroutine after
// the HTTP server is already listening.
//
// onReady (optional) is invoked once after the sequence completes successfully,
// so the caller can push a final SSE snapshot / kick off anything that needs
// MediaMTX up.
func runBoot(onReady func()) {
	// Phase 1: free ports / kill stale processes from a previous run.
	initMgr.SetPhase("cleanup", "Cleaning up previous instances and freeing ports…")
	killOldProcesses()

	// Phase 2: ensure media binaries (downloads ~150MB on first run only).
	// The downloader reports "downloading" vs "already present" via media.SetInitProgress.
	initMgr.SetPhase("binaries", "Checking media binaries…")
	if err := media.EnsureBinaries(); err != nil {
		initMgr.Fail("error", "Failed to prepare media binaries: "+err.Error())
		return
	}

	// Phase 3: start MediaMTX and wait for its streaming ports.
	initMgr.SetPhase("mediamtx", "Starting MediaMTX and waiting for streaming ports…")
	if err := media.StartMediaMTX(); err != nil {
		initMgr.Fail("error", "MediaMTX failed to start: "+err.Error())
		return
	}

	initMgr.MarkReady()
	if onReady != nil {
		onReady()
	}
}

// startInitBroadcaster pushes a status snapshot every 500ms while the boot is in
// progress so the spinner message updates live. It broadcasts once more after the
// boot finishes (success or error) and then stops.
func startInitBroadcaster(broadcast func()) {
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			broadcast()
			if initMgr.IsReady() {
				// Final push already sent this tick; stop the init ticker.
				return
			}
		}
	}()
}
