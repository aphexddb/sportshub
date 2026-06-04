package main

import (
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"

	"sportshub2/pkg/media"
)

// ===================== Per-camera state machine =====================
//
// Every camera the app touches gets exactly one Camera with an explicit lifecycle.
// All transitions go through the CameraManager under a single lock, which makes the
// invalid states we used to hit structurally impossible:
//
//   - GameChanger can only start once capture has reached StateLive, and StateLive is
//     only entered after MediaMTX confirms the publisher is ready (waitForPathReady).
//     => no more "no one is publishing to path camN" race.
//   - Stop drives Stopping -> Idle and broadcasts at each step, killing GC + ingest.
//     => the UI always reflects reality.
//   - A GC process dying no longer force-kills capture; teardown is decided by demand
//     (wantLocal / wantGC), not by scraping ffmpeg stderr.
//
// Capture lifecycle (drives the local SRT publish into MediaMTX):
//
//   Idle ──StartLocal/StartGC──> Starting ──publisher ready──> Live
//     ^                              │                           │
//     │                              └── timeout/exit ──> Error  │
//     └────────────── Stop ◄── Stopping ◄────────────── Stop ────┘
//
// GameChanger is a sub-phase that can only run while capture is Live:
//
//   GCIdle ──startGCNow──> GCStarting ──egress frames──> GCStreaming
//      ^                       │                              │
//      └─ Stop/exit ◄──────────┴── exit/err ──> GCError ──────┘

type CamState string

const (
	StateIdle     CamState = "idle"     // device known, nothing running
	StateStarting CamState = "starting" // capture launching + awaiting MediaMTX publisher
	StateLive     CamState = "live"     // capture publishing; local stream available
	StateStopping CamState = "stopping" // tearing down
	StateError    CamState = "error"    // failed; resettable by starting again
)

type GCPhase string

const (
	GCIdle      GCPhase = "idle"
	GCStarting  GCPhase = "starting"  // pull launching after capture went live
	GCStreaming GCPhase = "streaming" // actively pushing to GameChanger
	GCError     GCPhase = "error"     // push ended unexpectedly
)

type Camera struct {
	RawID string
	Name  string
	Path  string // clean MediaMTX path (cam0, cam1, ...); assigned on first start, stable after

	State CamState
	GC    GCPhase
	Err   string

	// demand: capture should run while either is true
	wantLocal bool
	wantGC    bool

	// GameChanger push
	gcCmd    *exec.Cmd
	gcDest   string
	gcEgress media.StreamStats
	lastGC   *GCConfig // remembered config for "Restart GC (last)" (in-memory only)

	// generation counters invalidate stale async transitions after a restart/stop
	gen   int // capture generation
	gcGen int // GC generation
}

type CameraManager struct {
	mu       sync.Mutex
	cams     map[string]*Camera
	nextIdx  int
	onChange func() // called (without the lock held) after any state change, e.g. broadcastStatus
}

func NewCameraManager(onChange func()) *CameraManager {
	return &CameraManager{cams: make(map[string]*Camera), onChange: onChange}
}

func (m *CameraManager) notify() {
	if m.onChange != nil {
		m.onChange()
	}
}

func (m *CameraManager) logState(c *Camera, msg string) {
	log.Printf("[cam %s] %s (state=%s gc=%s)", c.RawID, msg, c.State, c.GC)
}

// getLocked returns (creating if needed) the camera for a raw device id.
func (m *CameraManager) getLocked(rawID, name string) *Camera {
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

// resolve maps a frontend "cameraPath" (which may be a clean path like "cam0" or a raw
// device id like "video=Logitech BRIO") to the stable raw device id.
func (m *CameraManager) resolve(cameraPath string) string {
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
func (m *CameraManager) StartLocal(rawID, name string) {
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
func (m *CameraManager) StartGC(rawID, name, dest string, cfg GCConfig) error {
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
func (m *CameraManager) StopActiveGC() {
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
func (m *CameraManager) StopGC(rawID string) {
	m.mu.Lock()
	c := m.cams[rawID]
	if c == nil {
		m.mu.Unlock()
		return
	}
	c.wantGC = false
	cmd := c.gcCmd
	c.gcCmd = nil
	c.GC = GCIdle
	c.gcEgress = media.StreamStats{}
	c.gcGen++
	m.logState(c, "GC stop requested")
	needStop := !c.wantLocal
	m.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if needStop {
		m.Stop(rawID)
	}
	m.notify()
}

// Stop fully tears a device down: kills GC + capture and returns it to Idle.
func (m *CameraManager) Stop(rawID string) {
	m.mu.Lock()
	c := m.cams[rawID]
	if c == nil {
		m.mu.Unlock()
		return
	}
	c.wantLocal = false
	c.wantGC = false
	gcCmd := c.gcCmd
	c.gcCmd = nil
	c.GC = GCIdle
	c.gcEgress = media.StreamStats{}
	c.State = StateStopping
	c.gen++   // invalidate any in-flight capture startup
	c.gcGen++ // invalidate any in-flight GC startup
	m.logState(c, "stopping")
	m.mu.Unlock()

	if gcCmd != nil && gcCmd.Process != nil {
		_ = gcCmd.Process.Kill()
	}
	media.StopIngest(rawID) // synchronous: removes from ingest map + fires ingest-stopped

	m.mu.Lock()
	if c := m.cams[rawID]; c != nil && c.State == StateStopping {
		c.State = StateIdle
		c.Err = ""
		m.logState(c, "idle")
	}
	m.mu.Unlock()
	m.notify()
}

// onIngestStopped is called by the media notifier when an ingest process exits. If we
// didn't initiate it (state is Live/Starting, not Stopping), the camera died unexpectedly
// (e.g. unplugged) — surface that as Error and drop demand.
func (m *CameraManager) onIngestStopped(rawID string) {
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
	gcCmd := c.gcCmd
	c.gcCmd = nil
	c.GC = GCIdle
	c.gcEgress = media.StreamStats{}
	c.wantLocal = false
	c.wantGC = false
	c.State = StateError
	c.Err = "capture ended unexpectedly"
	c.gen++
	c.gcGen++
	m.logState(c, "ERROR: capture ended unexpectedly")
	m.mu.Unlock()

	if gcCmd != nil && gcCmd.Process != nil {
		_ = gcCmd.Process.Kill()
	}
	m.notify()
}

// ---------------- Internal transitions ----------------

// reconcileLocked nudges capture toward the desired state given current demand. Must hold m.mu.
func (m *CameraManager) reconcileLocked(c *Camera) {
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
func (m *CameraManager) beginStartLocked(c *Camera) {
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

// runStart launches the ingest ffmpeg and waits for MediaMTX to register the publisher
// before declaring the camera Live. Runs on its own goroutine (no lock held).
func (m *CameraManager) runStart(rawID, path string, gen int) {
	if err := media.StartIngestForCamera(rawID, path); err != nil {
		m.failCapture(rawID, gen, "ingest start failed: "+err.Error())
		return
	}

	ready := waitForPathReady(path, 60*time.Second)

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
		media.StopIngest(rawID)
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

func (m *CameraManager) failCapture(rawID string, gen int, msg string) {
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

// startGCNow launches the GameChanger pull. Precondition: capture is Live. Runs the
// ffmpeg restream and monitors it; teardown decisions are demand-based, not stderr-based.
func (m *CameraManager) startGCNow(rawID string) {
	m.mu.Lock()
	c := m.cams[rawID]
	if c == nil || c.State != StateLive || !c.wantGC || c.GC != GCIdle {
		m.mu.Unlock()
		return
	}
	c.GC = GCStarting
	c.gcEgress = media.StreamStats{}
	c.gcGen++
	gcGen := c.gcGen
	path, dest := c.Path, c.gcDest
	m.logState(c, "GC starting")
	m.mu.Unlock()
	m.notify()

	ffmpegPath, err := media.GetFFmpegPath()
	if err != nil {
		m.failGC(rawID, gcGen, "ffmpeg not found: "+err.Error())
		return
	}

	enc := gcParamsForQuality(currentQuality())
	args := buildGCArgs(path, dest, enc)
	log.Printf("[gamechanger] %s pull cam=%s → %s", rawID, path, dest)
	log.Printf("[gamechanger] ffmpeg %v", args)

	cmd := exec.Command(ffmpegPath, args...)
	stderrPipe, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		m.failGC(rawID, gcGen, "ffmpeg start failed: "+err.Error())
		return
	}

	// Commit the cmd handle (unless we've been superseded in the meantime).
	m.mu.Lock()
	c = m.cams[rawID]
	if c == nil || c.gcGen != gcGen {
		m.mu.Unlock()
		_ = cmd.Process.Kill()
		return
	}
	c.gcCmd = cmd
	m.mu.Unlock()

	go m.readGCStderr(rawID, gcGen, stderrPipe)
	go m.monitorGC(rawID, gcGen, cmd)
}

func (m *CameraManager) readGCStderr(rawID string, gcGen int, pipe io.ReadCloser) {
	if pipe == nil {
		return
	}
	buf := make([]byte, 4096)
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			s := string(buf[:n])
			log.Printf("[gamechanger ffmpeg stderr] %s", s)
			fps, br, spd, fr := media.ParseFFmpegProgressLine(s)
			if fps != 0 || br != "" || fr != 0 {
				m.mu.Lock()
				if c := m.cams[rawID]; c != nil && c.gcGen == gcGen {
					if fps != 0 {
						c.gcEgress.FPS = fps
					}
					if br != "" {
						c.gcEgress.Bitrate = br
					}
					if spd != "" {
						c.gcEgress.Speed = spd
					}
					if fr != 0 {
						c.gcEgress.Frames = fr
					}
					if c.GC == GCStarting && fr > 0 {
						c.GC = GCStreaming
						m.logState(c, "GC streaming")
					}
				}
				m.mu.Unlock()
				m.notify()
			}
		}
		if err != nil {
			return
		}
	}
}

func (m *CameraManager) monitorGC(rawID string, gcGen int, cmd *exec.Cmd) {
	waitErr := cmd.Wait()
	m.mu.Lock()
	c := m.cams[rawID]
	if c == nil || c.gcCmd != cmd || c.gcGen != gcGen {
		m.mu.Unlock() // superseded by a stop/restart
		return
	}
	intentional := !c.wantGC
	c.gcCmd = nil
	c.gcEgress = media.StreamStats{}
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

func (m *CameraManager) failGC(rawID string, gcGen int, msg string) {
	m.mu.Lock()
	c := m.cams[rawID]
	if c == nil || c.gcGen != gcGen {
		m.mu.Unlock()
		return
	}
	c.GC = GCError
	c.Err = msg
	c.wantGC = false
	c.gcCmd = nil
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

type gcGlobal struct {
	Active bool
	Path   string
	RawID  string
}

// Snapshot returns the per-device status list plus the global GC summary. Ingest stats are
// merged from the media layer; egress stats come from the camera's GC reader.
func (m *CameraManager) Snapshot() ([]DeviceStatus, gcGlobal) {
	ingestStats := media.GetStreamStats()

	m.mu.Lock()
	defer m.mu.Unlock()

	devs := make([]DeviceStatus, 0, len(m.cams))
	var g gcGlobal
	for _, c := range m.cams {
		ds := DeviceStatus{
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

// ---------------- helpers ----------------

func currentQuality() string {
	gcQualityMu.Lock()
	defer gcQualityMu.Unlock()
	return gcQuality
}

// buildGCArgs builds the GameChanger restream ffmpeg command (SRT pull → re-encode → FLV/RTMP).
func buildGCArgs(path, dest string, enc gcEncodeParams) []string {
	source := fmt.Sprintf("srt://127.0.0.1:8890?streamid=read:%s&latency=30000&mode=caller", path)
	return []string{
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		// NOTE: never "-avioflags direct" on SRT input — it forces tiny unbuffered reads that
		// libsrt rejects ("buffer size too small for the maximum possible 1316").
		"-probesize", "500000",
		"-analyzeduration", "1000000",
		"-err_detect", "ignore_err",
		"-i", source,
		"-vf", "scale=" + enc.scale,
		"-r", "30",
		"-c:v", "libx264",
		"-preset", "faster",
		"-profile:v", "high",
		"-level", enc.level,
		"-b:v", enc.bv,
		"-maxrate", enc.maxrate,
		"-bufsize", enc.bufsize,
		"-g", "60",
		"-keyint_min", "60",
		"-sc_threshold", "0",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-profile:a", "aac_low",
		"-b:a", "160k",
		"-ar", "48000",
		"-ac", "2",
		"-f", "flv",
		dest,
	}
}
