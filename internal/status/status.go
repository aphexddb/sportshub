// Package status holds the plain data types that flow between the media services,
// the camera state machine, and the HTTP/SSE layer. It is dependency-free (imports
// only the standard library) so every other package can share these shapes without
// creating import cycles. Behaviour lives elsewhere; this package is data only.
package status

import "time"

// StreamStats is a snapshot of ffmpeg progress for one stream (ingest or egress).
// Field names and JSON tags match the original wire contract the web UI consumes.
type StreamStats struct {
	FPS     float64 `json:"fps"`
	Bitrate string  `json:"bitrate"`
	Speed   string  `json:"speed"`
	Frames  int     `json:"frames"`
}

// GCConfig is the remembered GameChanger destination for a camera (in-memory only).
type GCConfig struct {
	FullURL string `json:"fullUrl,omitempty"`
	Server  string `json:"server,omitempty"`
	Key     string `json:"key,omitempty"`
	RawID   string `json:"rawId,omitempty"`
}

// ServerConfig is returned by GET /api/config so phones/LAN clients can build URLs.
type ServerConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	RTMPPort int    `json:"rtmpPort"`
	HLSPort  int    `json:"hlsPort"`
}

// DeviceStatus is the per-camera view pushed over SSE and returned by /api/active-streams.
type DeviceStatus struct {
	RawID       string       `json:"rawId"`
	Name        string       `json:"name"`
	Path        string       `json:"path,omitempty"`
	State       string       `json:"state"`           // camera state machine: idle/starting/live/stopping/error
	GCPhase     string       `json:"gcPhase"`         // GC sub-state: idle/starting/streaming/error
	Error       string       `json:"error,omitempty"` // last error message, if State/GCPhase is error
	LocalActive bool         `json:"localActive"`
	GCActive    bool         `json:"gcActive"`
	GCLast      *GCConfig    `json:"gcLast,omitempty"`
	Stats       *StreamStats `json:"stats,omitempty"`
	EgressStats *StreamStats `json:"egressStats,omitempty"`
}

// GlobalStatus is the server-wide view pushed over SSE.
type GlobalStatus struct {
	MediaMTXReady bool   `json:"mediaMTXReady"`
	ActiveIngests int    `json:"activeIngests"`
	GCActive      bool   `json:"gcActive"`
	GCPath        string `json:"gcPath,omitempty"`
	GCActiveRaw   string `json:"gcActiveRaw,omitempty"`
	GCQuality     string `json:"gcQuality"` // global broadcast quality: "1080p" / "720p" / "480p"
}

// InitCheck is one step of the startup sequence (port cleanup, binaries, media server, ...).
type InitCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`           // "pending" | "running" | "ok" | "error"
	Detail string `json:"detail,omitempty"` // human detail (e.g. error reason)
}

// InitState is the boot progress pushed over SSE so the UI can show a single loading spinner
// with a status message until the server is fully ready. Done flips true when boot finishes.
type InitState struct {
	Phase  string      `json:"phase"`           // current human-readable message for the spinner
	Done   bool        `json:"done"`            // true once boot completed (spinner can hide)
	Error  string      `json:"error,omitempty"` // set if boot failed
	Checks []InitCheck `json:"checks"`          // ordered per-step status
}

// Snapshot is the complete view pushed over SSE on /api/events.
type Snapshot struct {
	Ts      time.Time      `json:"ts"`
	Init    InitState      `json:"init"`
	Global  GlobalStatus   `json:"global"`
	Devices []DeviceStatus `json:"devices"`
}
