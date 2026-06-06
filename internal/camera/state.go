// Package camera owns every camera's lifecycle as an explicit state machine.
//
// All transitions go through the Manager under a single lock, which makes the
// invalid states this app used to hit structurally impossible:
//
//   - GameChanger can only start once capture has reached StateLive, and StateLive
//     is only entered after the media server confirms the publisher is ready.
//     => no more "no one is publishing to path camN" race.
//   - Stop drives Stopping -> Idle and notifies at each step, killing GC + ingest.
//     => the UI always reflects reality.
//   - A GC process dying no longer force-kills capture; teardown is decided by
//     demand (wantLocal / wantGC), not by scraping ffmpeg stderr.
//
// Capture lifecycle (drives the local SRT publish into the media server):
//
//	Idle ──StartLocal/StartGC──> Starting ──publisher ready──> Live
//	  ^                              │                           │
//	  │                              └── timeout/exit ──> Error  │
//	  └────────────── Stop ◄── Stopping ◄────────────── Stop ────┘
//
// GameChanger is a sub-phase that can only run while capture is Live:
//
//	GCIdle ──startGCNow──> GCStarting ──egress frames──> GCStreaming
//	   ^                       │                              │
//	   └─ Stop/exit ◄──────────┴── exit/err ──> GCError ──────┘
//
// The Manager depends only on small interfaces (MediaServer, Capturer, Restreamer)
// and the plain data types in internal/status, so the whole state machine is unit
// tested with fakes — no real ffmpeg or media server required.
package camera

import "sportshub/internal/status"

// CamState is the capture lifecycle state for one camera.
type CamState string

const (
	StateIdle     CamState = "idle"     // device known, nothing running
	StateStarting CamState = "starting" // capture launching + awaiting media-server publisher
	StateLive     CamState = "live"     // capture publishing; local stream available
	StateStopping CamState = "stopping" // tearing down
	StateError    CamState = "error"    // failed; resettable by starting again
)

// GCPhase is the GameChanger push sub-state (only meaningful while capture is Live).
type GCPhase string

const (
	GCIdle      GCPhase = "idle"
	GCStarting  GCPhase = "starting"  // push launching after capture went live
	GCStreaming GCPhase = "streaming" // actively pushing to GameChanger
	GCError     GCPhase = "error"     // push ended unexpectedly
)

// Camera is the per-device state. It is plain data; all behaviour lives on Manager.
type Camera struct {
	RawID string
	Name  string
	Path  string // clean media-server path (cam0, cam1, ...); assigned on first start, stable after

	State CamState
	GC    GCPhase
	Err   string

	// demand: capture should run while either is true
	wantLocal bool
	wantGC    bool

	// GameChanger push
	gcHandle RestreamHandle     // live push handle (nil when not pushing)
	gcDest   string             // current push destination
	gcEgress status.StreamStats // latest egress progress
	lastGC   *status.GCConfig   // remembered config for "Restart GC (last)" (in-memory only)

	// generation counters invalidate stale async transitions after a restart/stop
	gen   int // capture generation
	gcGen int // GC generation
}
