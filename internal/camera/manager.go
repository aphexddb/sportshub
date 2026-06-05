package camera

import (
	"fmt"
	"log"
	"sync"
	"time"

	"sportshub2/internal/status"
)

// defaultReadyTimeout is how long capture waits for the media server to register
// the publisher before declaring the camera Error.
const defaultReadyTimeout = 60 * time.Second

// ---------------- Dependency seams (consumer-defined interfaces) ----------------

// MediaServer is the subset of the in-process media server the state machine needs:
// confirming when a published path is actually ready for readers.
type MediaServer interface {
	// WaitPathReady blocks until the path has a live publisher with parsed tracks,
	// or timeout elapses. Returns true iff the path became ready.
	WaitPathReady(path string, timeout time.Duration) bool
}

// Capturer drives the local capture ffmpeg (device -> SRT publish into the media server).
type Capturer interface {
	Start(rawID, path string) error
	Stop(rawID string)
	Stats() map[string]status.StreamStats
}

// Restreamer drives the GameChanger push (SRT pull -> re-encode -> RTMP/RTMPS push).
// It runs the process and reports progress/exit back through the request callbacks,
// which the Manager uses to drive GC phase transitions.
type Restreamer interface {
	Start(req RestreamRequest) (RestreamHandle, error)
}

// RestreamHandle controls a running GameChanger push.
type RestreamHandle interface {
	Stop()
}

// RestreamRequest is everything the Restreamer needs to launch one push. OnStats and
// OnExit are invoked from the Restreamer's own goroutines; the Manager re-validates the
// generation under its lock before acting, so stale callbacks are safely ignored.
type RestreamRequest struct {
	RawID   string
	Path    string
	Dest    string
	Quality string
	// PreviewPath is a local media-server path the restreamer should also publish the
	// re-encoded GameChanger feed to (a copy), so the UI can preview exactly what's being
	// pushed. Empty means "no preview".
	PreviewPath string
	OnStats     func(status.StreamStats)
	OnExit      func(error)
}

// egressPath is the local preview path for a camera's GameChanger feed (a copy of what's
// pushed). Convention shared by startGCNow (where it's published) and Snapshot (where it's
// exposed to the UI).
func egressPath(capturePath string) string {
	if capturePath == "" {
		return ""
	}
	return capturePath + "gc"
}

// ---------------- Manager ----------------

type Manager struct {
	mu      sync.Mutex
	cams    map[string]*Camera
	nextIdx int

	media    MediaServer
	capture  Capturer
	restream Restreamer

	onChange     func() // called (without the lock held) after any state change, e.g. broadcastStatus
	readyTimeout time.Duration

	qmu     sync.Mutex
	quality string // global broadcast quality: "1080p" (default) / "720p" / "480p"
}

// NewManager builds a state machine over the given media/capture/restream services.
// onChange is invoked (lock not held) after every transition so callers can broadcast.
func NewManager(media MediaServer, capture Capturer, restream Restreamer, onChange func()) *Manager {
	return &Manager{
		cams:         make(map[string]*Camera),
		media:        media,
		capture:      capture,
		restream:     restream,
		onChange:     onChange,
		readyTimeout: defaultReadyTimeout,
		quality:      "1080p",
	}
}

func (m *Manager) notify() {
	if m.onChange != nil {
		m.onChange()
	}
}

func (m *Manager) logState(c *Camera, msg string) {
	log.Printf("[cam %s] %s (state=%s gc=%s)", c.RawID, msg, c.State, c.GC)
}

// Quality returns the current global broadcast quality.
func (m *Manager) Quality() string {
	m.qmu.Lock()
	defer m.qmu.Unlock()
	return m.quality
}

// SetQuality updates the global broadcast quality (coerced by the caller if needed).
func (m *Manager) SetQuality(q string) {
	m.qmu.Lock()
	m.quality = q
	m.qmu.Unlock()
}

// getLocked returns (creating if needed) the camera for a raw device id.
func (m *Manager) getLocked(rawID, name string) *Camera {
	c := m.cams[rawID]
	if c == nil {
		c = &Camera{RawID: rawID, Name: rawID, State: StateIdle, GC: GCIdle}
		m.cams[rawID] = c
	}
	if name != "" {
		c.Name = name
	}
	return c
}

// Resolve maps a frontend "cameraPath" (which may be a clean path like "cam0" or a raw
// device id like "video=Logitech BRIO") to the stable raw device id.
func (m *Manager) Resolve(cameraPath string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.cams {
		if c.RawID == cameraPath || (c.Path != "" && c.Path == cameraPath) {
			return c.RawID
		}
	}
	return cameraPath
}

// ---------------- Public API ----------------

// StartLocal marks a device as wanted for local streaming and kicks off capture if idle.
func (m *Manager) StartLocal(rawID, name string) {
	m.mu.Lock()
	c := m.getLocked(rawID, name)
	c.wantLocal = true
	m.reconcileLocked(c)
	m.mu.Unlock()
	m.notify()
}

// StartGC marks a device as wanted for GameChanger and starts capture if needed. The
// actual GC push is deferred until capture reaches StateLive (publisher ready). Only one
// camera may push to GameChanger at a time. Returns an error if another camera is active.
func (m *Manager) StartGC(rawID, name, dest string, cfg status.GCConfig) error {
	m.mu.Lock()
	for _, other := range m.cams {
		if other.RawID != rawID && other.GC != GCIdle {
			m.mu.Unlock()
			return fmt.Errorf("GameChanger already active for %s", other.Name)
		}
	}
	c := m.getLocked(rawID, name)
	c.wantGC = true
	c.gcDest = dest
	cfgCopy := cfg
	c.lastGC = &cfgCopy
	m.reconcileLocked(c)
	// If we're already live and GC isn't running, fire it now (outside the lock).
	fire := c.State == StateLive && c.GC == GCIdle
	if fire {
		m.logState(c, "GC requested → starting now (capture already live)")
	} else {
		m.logState(c, "GC requested → deferred until capture is live")
	}
	m.mu.Unlock()
	if fire {
		m.startGCNow(rawID)
	}
	m.notify()
	return nil
}

// StopActiveGC stops whichever camera is currently pushing to GameChanger (single-GC model).
func (m *Manager) StopActiveGC() {
	m.mu.Lock()
	var target string
	for _, c := range m.cams {
		if c.GC != GCIdle {
			target = c.RawID
			break
		}
	}
	m.mu.Unlock()
	if target != "" {
		m.StopGC(target)
	}
}

// StopGC stops the GameChanger push for a device but leaves local capture running if it's
// still wanted locally; otherwise tears the capture down too.
func (m *Manager) StopGC(rawID string) {
	m.mu.Lock()
	c := m.cams[rawID]
	if c == nil {
		m.mu.Unlock()
		return
	}
	c.wantGC = false
	h := c.gcHandle
	c.gcHandle = nil
	c.GC = GCIdle
	c.gcEgress = status.StreamStats{}
	c.gcGen++
	m.logState(c, "GC stop requested")
	needStop := !c.wantLocal
	m.mu.Unlock()

	if h != nil {
		h.Stop()
	}
	if needStop {
		m.Stop(rawID)
	}
	m.notify()
}

// Stop fully tears a device down: kills GC + capture and returns it to Idle.
func (m *Manager) Stop(rawID string) {
	m.mu.Lock()
	c := m.cams[rawID]
	if c == nil {
		m.mu.Unlock()
		return
	}
	c.wantLocal = false
	c.wantGC = false
	h := c.gcHandle
	c.gcHandle = nil
	c.GC = GCIdle
	c.gcEgress = status.StreamStats{}
	c.State = StateStopping
	c.gen++   // invalidate any in-flight capture startup
	c.gcGen++ // invalidate any in-flight GC startup
	m.logState(c, "stopping")
	m.mu.Unlock()

	if h != nil {
		h.Stop()
	}
	m.capture.Stop(rawID) // synchronous: removes from active set

	m.mu.Lock()
	if c := m.cams[rawID]; c != nil && c.State == StateStopping {
		c.State = StateIdle
		c.Err = ""
		m.logState(c, "idle")
	}
	m.mu.Unlock()
	m.notify()
}

// StopAll tears down every known camera (kills each GC push + capture). Used on shutdown so
// no ffmpeg child outlives the app. Snapshots the ids under the lock, then stops each without
// holding it (Stop takes the lock itself).
func (m *Manager) StopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.cams))
	for id := range m.cams {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Stop(id)
	}
}

// HandleIngestExit is called when a capture process exits. If we didn't initiate it
// (state is Live/Starting, not Stopping), the camera died unexpectedly (e.g. unplugged) —
// surface that as Error and drop demand. Wire this to the Capturer's exit callback.
func (m *Manager) HandleIngestExit(rawID string) {
	m.mu.Lock()
	c := m.cams[rawID]
	if c == nil || (c.State != StateLive && c.State != StateStarting) {
		if c != nil {
			m.mu.Unlock()
			m.notify() // still refresh UI (e.g. our own teardown)
			return
		}
		m.mu.Unlock()
		return
	}
	h := c.gcHandle
	c.gcHandle = nil
	c.GC = GCIdle
	c.gcEgress = status.StreamStats{}
	c.wantLocal = false
	c.wantGC = false
	c.State = StateError
	c.Err = "capture ended unexpectedly"
	c.gen++
	c.gcGen++
	m.logState(c, "ERROR: capture ended unexpectedly")
	m.mu.Unlock()

	if h != nil {
		h.Stop()
	}
	m.notify()
}

// ---------------- Internal transitions ----------------

// reconcileLocked nudges capture toward the desired state given current demand. Must hold m.mu.
func (m *Manager) reconcileLocked(c *Camera) {
	want := c.wantLocal || c.wantGC
	switch c.State {
	case StateIdle, StateError:
		if want {
			m.beginStartLocked(c)
		}
	case StateLive:
		if !want {
			// caller handles teardown via Stop(); nothing to do inline here
		}
	}
}

// beginStartLocked assigns a path, enters Starting, and launches capture asynchronously.
func (m *Manager) beginStartLocked(c *Camera) {
	if c.Path == "" {
		c.Path = fmt.Sprintf("cam%d", m.nextIdx)
		m.nextIdx++
	}
	c.State = StateStarting
	c.Err = ""
	c.gen++
	gen := c.gen
	rawID, path := c.RawID, c.Path
	m.logState(c, "starting capture")
	go m.runStart(rawID, path, gen)
}

// runStart launches capture and waits for the media server to register the publisher
// before declaring the camera Live. Runs on its own goroutine (no lock held).
func (m *Manager) runStart(rawID, path string, gen int) {
	if err := m.capture.Start(rawID, path); err != nil {
		m.failCapture(rawID, gen, "ingest start failed: "+err.Error())
		return
	}

	ready := m.media.WaitPathReady(path, m.readyTimeout)

	m.mu.Lock()
	c := m.cams[rawID]
	if c == nil || c.gen != gen {
		m.mu.Unlock() // superseded by a newer start/stop
		return
	}
	if !ready {
		c.State = StateError
		c.Err = "publisher never became ready (camera failed to start?)"
		c.gen++
		m.logState(c, "ERROR: publisher not ready")
		m.mu.Unlock()
		m.capture.Stop(rawID)
		m.notify()
		return
	}
	c.State = StateLive
	c.Err = ""
	m.logState(c, "live")
	startGC := c.wantGC && c.GC == GCIdle
	m.mu.Unlock()
	m.notify()

	if startGC {
		m.startGCNow(rawID)
	}
}

func (m *Manager) failCapture(rawID string, gen int, msg string) {
	m.mu.Lock()
	c := m.cams[rawID]
	if c == nil || c.gen != gen {
		m.mu.Unlock()
		return
	}
	c.State = StateError
	c.Err = msg
	c.gen++
	m.logState(c, "ERROR: "+msg)
	m.mu.Unlock()
	m.notify()
}

// startGCNow launches the GameChanger push. Precondition: capture is Live. Teardown
// decisions are demand-based, not stderr-based.
func (m *Manager) startGCNow(rawID string) {
	m.mu.Lock()
	c := m.cams[rawID]
	if c == nil || c.State != StateLive || !c.wantGC || c.GC != GCIdle {
		m.mu.Unlock()
		return
	}
	c.GC = GCStarting
	c.gcEgress = status.StreamStats{}
	c.gcGen++
	gcGen := c.gcGen
	path, dest := c.Path, c.gcDest
	m.logState(c, "GC starting")
	m.mu.Unlock()
	m.notify()

	req := RestreamRequest{
		RawID:       rawID,
		Path:        path,
		Dest:        dest,
		Quality:     m.Quality(),
		PreviewPath: egressPath(path),
		OnStats:     func(s status.StreamStats) { m.onGCStats(rawID, gcGen, s) },
		OnExit:      func(err error) { m.onGCExit(rawID, gcGen, err) },
	}

	handle, err := m.restream.Start(req)
	if err != nil {
		m.failGC(rawID, gcGen, "restream start failed: "+err.Error())
		return
	}

	// Commit the handle (unless we've been superseded in the meantime).
	m.mu.Lock()
	c = m.cams[rawID]
	if c == nil || c.gcGen != gcGen {
		m.mu.Unlock()
		handle.Stop()
		return
	}
	c.gcHandle = handle
	m.mu.Unlock()
}

// onGCStats records egress progress and promotes GCStarting -> GCStreaming on first frames.
func (m *Manager) onGCStats(rawID string, gcGen int, s status.StreamStats) {
	m.mu.Lock()
	c := m.cams[rawID]
	if c == nil || c.gcGen != gcGen {
		m.mu.Unlock()
		return
	}
	c.gcEgress = s
	if c.GC == GCStarting && s.Frames > 0 {
		c.GC = GCStreaming
		m.logState(c, "GC streaming")
	}
	m.mu.Unlock()
	m.notify()
}

// onGCExit handles the push process ending: intentional (wantGC cleared) -> Idle,
// otherwise -> Error. Tears capture down if it's no longer wanted locally.
func (m *Manager) onGCExit(rawID string, gcGen int, waitErr error) {
	m.mu.Lock()
	c := m.cams[rawID]
	if c == nil || c.gcGen != gcGen {
		m.mu.Unlock() // superseded by a stop/restart
		return
	}
	intentional := !c.wantGC
	c.gcHandle = nil
	c.gcEgress = status.StreamStats{}
	c.gcGen++
	if intentional {
		c.GC = GCIdle
		m.logState(c, fmt.Sprintf("GC stopped (%v)", waitErr))
	} else {
		c.GC = GCError
		c.wantGC = false
		c.Err = "GameChanger stream ended"
		m.logState(c, fmt.Sprintf("GC ERROR exited: %v", waitErr))
	}
	needStop := !c.wantLocal && c.State == StateLive
	m.mu.Unlock()
	m.notify()
	if needStop {
		m.Stop(rawID)
	}
}

func (m *Manager) failGC(rawID string, gcGen int, msg string) {
	m.mu.Lock()
	c := m.cams[rawID]
	if c == nil || c.gcGen != gcGen {
		m.mu.Unlock()
		return
	}
	c.GC = GCError
	c.Err = msg
	c.wantGC = false
	c.gcHandle = nil
	c.gcGen++
	needStop := !c.wantLocal && c.State == StateLive
	m.logState(c, "GC ERROR: "+msg)
	m.mu.Unlock()
	m.notify()
	if needStop {
		m.Stop(rawID)
	}
}

// ---------------- Snapshot for status/SSE ----------------

// GCGlobal is the single-GC global summary derived from the per-camera state.
type GCGlobal struct {
	Active bool
	Path   string
	RawID  string
}

// Snapshot returns the per-device status list plus the global GC summary. Ingest stats are
// merged from the capturer; egress stats come from the camera's GC reader.
func (m *Manager) Snapshot() ([]status.DeviceStatus, GCGlobal) {
	ingestStats := m.capture.Stats()

	m.mu.Lock()
	defer m.mu.Unlock()

	devs := make([]status.DeviceStatus, 0, len(m.cams))
	var g GCGlobal
	for _, c := range m.cams {
		ds := status.DeviceStatus{
			RawID:   c.RawID,
			Name:    c.Name,
			State:   string(c.State),
			GCPhase: string(c.GC),
			Error:   c.Err,
		}
		// Only expose a path (which the UI treats as "streamable now") once live.
		if c.State == StateLive {
			ds.Path = c.Path
			ds.LocalActive = true
		}
		if c.GC == GCStreaming {
			ds.GCActive = true
			ds.EgressPath = egressPath(c.Path) // local preview of the GameChanger feed
			g.Active = true
			g.Path = c.Path
			g.RawID = c.RawID
		}
		if c.lastGC != nil {
			lc := *c.lastGC
			ds.GCLast = &lc
		}
		if st, ok := ingestStats[c.RawID]; ok && (st.FPS != 0 || st.Bitrate != "") {
			cp := st
			ds.Stats = &cp
		}
		if (c.GC == GCStreaming || c.GC == GCStarting) && (c.gcEgress.FPS != 0 || c.gcEgress.Bitrate != "") {
			cp := c.gcEgress
			ds.EgressStats = &cp
		}
		devs = append(devs, ds)
	}
	return devs, g
}
