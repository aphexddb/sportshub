package camera

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sportshub2/internal/status"
)

// ---------------- fakes ----------------

type fakeMedia struct {
	ready bool
	delay time.Duration
	calls int32
}

func (f *fakeMedia) WaitPathReady(path string, timeout time.Duration) bool {
	atomic.AddInt32(&f.calls, 1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return f.ready
}

type fakeCapturer struct {
	mu       sync.Mutex
	started  map[string]string
	stopped  []string
	startErr error
	stats    map[string]status.StreamStats
}

func newFakeCapturer() *fakeCapturer {
	return &fakeCapturer{started: map[string]string{}, stats: map[string]status.StreamStats{}}
}

func (f *fakeCapturer) Start(rawID, path string) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.mu.Lock()
	f.started[rawID] = path
	f.mu.Unlock()
	return nil
}

func (f *fakeCapturer) Stop(rawID string) {
	f.mu.Lock()
	f.stopped = append(f.stopped, rawID)
	delete(f.started, rawID)
	f.mu.Unlock()
}

func (f *fakeCapturer) Stats() map[string]status.StreamStats {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]status.StreamStats, len(f.stats))
	for k, v := range f.stats {
		out[k] = v
	}
	return out
}

func (f *fakeCapturer) stopCount(rawID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.stopped {
		if s == rawID {
			n++
		}
	}
	return n
}

type fakeHandle struct{ stopped int32 }

func (h *fakeHandle) Stop() { atomic.AddInt32(&h.stopped, 1) }

type fakeRestreamer struct {
	mu       sync.Mutex
	started  int
	lastReq  RestreamRequest
	handle   *fakeHandle
	startErr error
}

func (f *fakeRestreamer) Start(req RestreamRequest) (RestreamHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return nil, f.startErr
	}
	f.started++
	f.lastReq = req
	f.handle = &fakeHandle{}
	return f.handle, nil
}

func (f *fakeRestreamer) req() RestreamRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastReq
}

// waitHandle blocks (briefly) until Start has created a handle, then returns it. The GC
// phase flips to GCStarting before startGCNow commits the handle, so tests must wait for
// the handle rather than reading the field right after observing GCStarting.
func (f *fakeRestreamer) waitHandle() *fakeHandle {
	for i := 0; i < 500; i++ {
		f.mu.Lock()
		h := f.handle
		f.mu.Unlock()
		if h != nil {
			return h
		}
		time.Sleep(2 * time.Millisecond)
	}
	return nil
}

func handleStopped(h *fakeHandle) bool { return h != nil && atomic.LoadInt32(&h.stopped) != 0 }

// ---------------- helpers ----------------

func newTestManager(media MediaServer, cap Capturer, re Restreamer) *Manager {
	m := NewManager(media, cap, re, func() {})
	m.readyTimeout = 500 * time.Millisecond // keep "not ready" tests fast
	return m
}

func snap(m *Manager, rawID string) (CamState, GCPhase, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.cams[rawID]
	if c == nil {
		return "", "", ""
	}
	return c.State, c.GC, c.Err
}

func eventually(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", msg)
}

func waitState(t *testing.T, m *Manager, rawID string, want CamState) {
	t.Helper()
	eventually(t, time.Second, func() bool {
		s, _, _ := snap(m, rawID)
		return s == want
	}, "state == "+string(want))
}

func waitGC(t *testing.T, m *Manager, rawID string, want GCPhase) {
	t.Helper()
	eventually(t, time.Second, func() bool {
		_, gc, _ := snap(m, rawID)
		return gc == want
	}, "gc == "+string(want))
}

const rawA = "video=A"
const rawB = "video=B"

// ---------------- tests ----------------

func TestStartLocal_GoesLiveAndAssignsPath(t *testing.T) {
	media := &fakeMedia{ready: true}
	cap := newFakeCapturer()
	m := newTestManager(media, cap, &fakeRestreamer{})

	m.StartLocal(rawA, "Cam A")
	waitState(t, m, rawA, StateLive)

	m.mu.Lock()
	path := m.cams[rawA].Path
	m.mu.Unlock()
	if path != "cam0" {
		t.Fatalf("expected path cam0, got %q", path)
	}
	cap.mu.Lock()
	gotPath := cap.started[rawA]
	cap.mu.Unlock()
	if gotPath != "cam0" {
		t.Fatalf("capturer should have been started on cam0, got %q", gotPath)
	}
}

func TestStartLocal_PublisherNeverReady_Errors(t *testing.T) {
	media := &fakeMedia{ready: false}
	cap := newFakeCapturer()
	m := newTestManager(media, cap, &fakeRestreamer{})

	m.StartLocal(rawA, "Cam A")
	waitState(t, m, rawA, StateError)

	if cap.stopCount(rawA) != 1 {
		t.Fatalf("capture should be stopped once when publisher not ready, got %d", cap.stopCount(rawA))
	}
}

func TestStartLocal_CaptureStartFails_Errors(t *testing.T) {
	media := &fakeMedia{ready: true}
	cap := newFakeCapturer()
	cap.startErr = errors.New("device busy")
	m := newTestManager(media, cap, &fakeRestreamer{})

	m.StartLocal(rawA, "Cam A")
	waitState(t, m, rawA, StateError)
	if _, _, errMsg := snap(m, rawA); errMsg == "" {
		t.Fatal("expected an error message on the camera")
	}
}

func TestStartGC_DefersUntilLiveThenStreams(t *testing.T) {
	media := &fakeMedia{ready: true}
	cap := newFakeCapturer()
	re := &fakeRestreamer{}
	m := newTestManager(media, cap, re)

	if err := m.StartGC(rawA, "Cam A", "rtmp://dest/app/key", status.GCConfig{}); err != nil {
		t.Fatalf("StartGC error: %v", err)
	}
	// capture must reach live, then GC auto-starts to GCStarting
	waitState(t, m, rawA, StateLive)
	waitGC(t, m, rawA, GCStarting)

	// simulate egress frames flowing -> GCStreaming
	re.req().OnStats(status.StreamStats{Frames: 5, FPS: 30, Bitrate: "6000kbits/s"})
	waitGC(t, m, rawA, GCStreaming)
}

func TestStartGC_FiresImmediatelyWhenAlreadyLive(t *testing.T) {
	media := &fakeMedia{ready: true}
	cap := newFakeCapturer()
	re := &fakeRestreamer{}
	m := newTestManager(media, cap, re)

	m.StartLocal(rawA, "Cam A")
	waitState(t, m, rawA, StateLive)

	if err := m.StartGC(rawA, "Cam A", "rtmp://dest", status.GCConfig{}); err != nil {
		t.Fatalf("StartGC error: %v", err)
	}
	waitGC(t, m, rawA, GCStarting)
	if re.started != 1 {
		t.Fatalf("expected restreamer started once, got %d", re.started)
	}
}

func TestStartGC_SingleOwnerRejectsSecondCamera(t *testing.T) {
	media := &fakeMedia{ready: true}
	cap := newFakeCapturer()
	re := &fakeRestreamer{}
	m := newTestManager(media, cap, re)

	if err := m.StartGC(rawA, "Cam A", "rtmp://dest", status.GCConfig{}); err != nil {
		t.Fatalf("first StartGC error: %v", err)
	}
	waitGC(t, m, rawA, GCStarting) // A now owns GC

	err := m.StartGC(rawB, "Cam B", "rtmp://dest2", status.GCConfig{})
	if err == nil {
		t.Fatal("expected second camera's StartGC to be rejected while A is active")
	}
}

func TestHandleIngestExit_OnLive_Errors(t *testing.T) {
	media := &fakeMedia{ready: true}
	cap := newFakeCapturer()
	m := newTestManager(media, cap, &fakeRestreamer{})

	m.StartLocal(rawA, "Cam A")
	waitState(t, m, rawA, StateLive)

	m.HandleIngestExit(rawA)
	waitState(t, m, rawA, StateError)
	if _, _, errMsg := snap(m, rawA); errMsg != "capture ended unexpectedly" {
		t.Fatalf("unexpected error message: %q", errMsg)
	}
}

func TestHandleIngestExit_DuringTeardown_Ignored(t *testing.T) {
	media := &fakeMedia{ready: true}
	cap := newFakeCapturer()
	m := newTestManager(media, cap, &fakeRestreamer{})

	m.StartLocal(rawA, "Cam A")
	waitState(t, m, rawA, StateLive)
	m.Stop(rawA)
	waitState(t, m, rawA, StateIdle)

	// A late exit callback from our own teardown must NOT flip the camera to Error.
	m.HandleIngestExit(rawA)
	if s, _, _ := snap(m, rawA); s != StateIdle {
		t.Fatalf("expected camera to remain Idle, got %s", s)
	}
}

func TestStop_KillsGCHandleAndCapture(t *testing.T) {
	media := &fakeMedia{ready: true}
	cap := newFakeCapturer()
	re := &fakeRestreamer{}
	m := newTestManager(media, cap, re)

	m.StartGC(rawA, "Cam A", "rtmp://dest", status.GCConfig{})
	waitGC(t, m, rawA, GCStarting)
	h := re.waitHandle()
	if h == nil {
		t.Fatal("restreamer never produced a handle")
	}

	m.Stop(rawA)
	waitState(t, m, rawA, StateIdle)

	// The handle is killed either inline by Stop or by startGCNow's supersede path, so wait.
	eventually(t, time.Second, func() bool { return handleStopped(h) }, "GC handle stopped")
	if cap.stopCount(rawA) == 0 {
		t.Fatal("capture should have been stopped")
	}
}

func TestStopGC_KeepsLocalWhenStillWanted(t *testing.T) {
	media := &fakeMedia{ready: true}
	cap := newFakeCapturer()
	re := &fakeRestreamer{}
	m := newTestManager(media, cap, re)

	m.StartLocal(rawA, "Cam A")
	waitState(t, m, rawA, StateLive)
	m.StartGC(rawA, "Cam A", "rtmp://dest", status.GCConfig{})
	waitGC(t, m, rawA, GCStarting)
	h := re.waitHandle()
	if h == nil {
		t.Fatal("restreamer never produced a handle")
	}

	m.StopGC(rawA)
	waitGC(t, m, rawA, GCIdle)

	eventually(t, time.Second, func() bool { return handleStopped(h) }, "GC handle stopped")
	if s, _, _ := snap(m, rawA); s != StateLive {
		t.Fatalf("capture should remain Live after StopGC, got %s", s)
	}
	if cap.stopCount(rawA) != 0 {
		t.Fatal("capture must NOT be stopped while still wanted locally")
	}
}

func TestStopGC_TearsDownWhenOnlyGCWanted(t *testing.T) {
	media := &fakeMedia{ready: true}
	cap := newFakeCapturer()
	re := &fakeRestreamer{}
	m := newTestManager(media, cap, re)

	m.StartGC(rawA, "Cam A", "rtmp://dest", status.GCConfig{})
	waitGC(t, m, rawA, GCStarting)

	m.StopGC(rawA)
	waitState(t, m, rawA, StateIdle)
	if cap.stopCount(rawA) == 0 {
		t.Fatal("capture should be torn down when only GC was wanted")
	}
}

func TestGCExit_UnexpectedWhileLocalWanted_GCError(t *testing.T) {
	media := &fakeMedia{ready: true}
	cap := newFakeCapturer()
	re := &fakeRestreamer{}
	m := newTestManager(media, cap, re)

	m.StartLocal(rawA, "Cam A")
	waitState(t, m, rawA, StateLive)
	m.StartGC(rawA, "Cam A", "rtmp://dest", status.GCConfig{})
	waitGC(t, m, rawA, GCStarting)

	// Push process dies on its own (we never asked it to stop).
	re.req().OnExit(errors.New("connection reset"))
	waitGC(t, m, rawA, GCError)
	if s, _, _ := snap(m, rawA); s != StateLive {
		t.Fatalf("capture should remain Live after GC error (local still wanted), got %s", s)
	}
}

func TestStartGC_RestreamStartFailure_GCError(t *testing.T) {
	media := &fakeMedia{ready: true}
	cap := newFakeCapturer()
	re := &fakeRestreamer{startErr: errors.New("ffmpeg missing")}
	m := newTestManager(media, cap, re)

	m.StartLocal(rawA, "Cam A")
	waitState(t, m, rawA, StateLive)
	m.StartGC(rawA, "Cam A", "rtmp://dest", status.GCConfig{})
	waitGC(t, m, rawA, GCError)
}

func TestResolveAndQuality(t *testing.T) {
	media := &fakeMedia{ready: true}
	cap := newFakeCapturer()
	m := newTestManager(media, cap, &fakeRestreamer{})

	m.StartLocal(rawA, "Cam A")
	waitState(t, m, rawA, StateLive)

	// raw id resolves to itself; clean path resolves to raw id
	if got := m.Resolve(rawA); got != rawA {
		t.Fatalf("Resolve(rawA)=%q", got)
	}
	if got := m.Resolve("cam0"); got != rawA {
		t.Fatalf("Resolve(cam0)=%q, want %q", got, rawA)
	}
	if got := m.Resolve("unknown"); got != "unknown" {
		t.Fatalf("unknown path should pass through, got %q", got)
	}

	if m.Quality() != "1080p" {
		t.Fatalf("default quality should be 1080p, got %q", m.Quality())
	}
	m.SetQuality("720p")
	if m.Quality() != "720p" {
		t.Fatalf("SetQuality not applied, got %q", m.Quality())
	}
}

func TestSnapshot_MergesIngestStats(t *testing.T) {
	media := &fakeMedia{ready: true}
	cap := newFakeCapturer()
	m := newTestManager(media, cap, &fakeRestreamer{})

	m.StartLocal(rawA, "Cam A")
	waitState(t, m, rawA, StateLive)

	cap.mu.Lock()
	cap.stats[rawA] = status.StreamStats{FPS: 30, Bitrate: "5000kbits/s", Frames: 100}
	cap.mu.Unlock()

	devs, g := m.Snapshot()
	if len(devs) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devs))
	}
	d := devs[0]
	if d.State != string(StateLive) || !d.LocalActive || d.Path != "cam0" {
		t.Fatalf("unexpected device snapshot: %+v", d)
	}
	if d.Stats == nil || d.Stats.FPS != 30 {
		t.Fatalf("ingest stats not merged: %+v", d.Stats)
	}
	if g.Active {
		t.Fatal("global GC should be inactive")
	}
}
