package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"sportshub2/pkg/media"
	"sportshub2/pkg/sources"
)

//go:embed static/hls.min.js
var hlsJS []byte

// Simple in-memory state for the Windows spike
type Camera struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Stream struct {
	CameraID  string `json:"cameraId"`
	Active    bool   `json:"active"`
	RTMP      string `json:"rtmp"`
	StartedAt string `json:"startedAt,omitempty"`
}

var (
	mu      sync.Mutex
	streams = make(map[string]*Stream) // key = raw camera ID from server list

	// serverStreams: clean path (e.g. "cam0") -> info
	serverStreams = make(map[string]ServerStreamInfo)
	nextCamIndex  int

	// GameChanger state (only one active at a time for now)
	gcMu         sync.Mutex
	gcActive     bool
	gcCmd        *exec.Cmd
	gcActivePath string // e.g. "cam0"
	gcCamera     string // raw or name for display

	// last used GC config per clean path, for easy restart
	gcLastConfigs = make(map[string]GCConfig)
)

var publicHost string // non-loopback IP for phone/LAN access (used for viewer URLs and QR codes)

type GCConfig struct {
	FullURL string `json:"fullUrl,omitempty"`
	Server  string `json:"server,omitempty"`
	Key     string `json:"key,omitempty"`
	RawID   string `json:"rawId,omitempty"`
}

type ServerStreamInfo struct {
	RawID   string `json:"rawId"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	GCActive bool `json:"gcActive"`
	GCLast   *GCConfig `json:"gcLast,omitempty"`
}

type demand struct {
	local bool
	gc    bool
}

var demands = make(map[string]*demand) // raw camera ID -> demand

func getDemand(raw string) *demand {
	if d, ok := demands[raw]; ok {
		return d
	}
	d := &demand{}
	demands[raw] = d
	return d
}

func ensureCapture(rawID string) string {
	d := getDemand(rawID)
	if !d.local && !d.gc {
		return ""
	}
	// find or assign path
	mu.Lock()
	path := ""
	for p, info := range serverStreams {
		if info.RawID == rawID {
			path = p
			break
		}
	}
	if path == "" {
		path = fmt.Sprintf("cam%d", nextCamIndex)
		nextCamIndex++
		serverStreams[path] = ServerStreamInfo{
			RawID: rawID,
			Name:  rawID,
			Path:  path,
		}
	}
	mu.Unlock()

	if !isCaptureRunning(rawID) {
		go func() {
			if err := media.StartIngestForCamera(rawID, path); err != nil {
				log.Printf("[capture] start ingest failed for %s: %v", rawID, err)
			}
		}()
	}
	return path
}

func cleanupCapture(rawID string) {
	d := getDemand(rawID)
	if d.local || d.gc {
		return
	}
	media.StopIngest(rawID)
	mu.Lock()
	for p, info := range serverStreams {
		if info.RawID == rawID {
			delete(serverStreams, p)
			break
		}
	}
	mu.Unlock()
}

func setLocalDemand(rawID string, on bool) {
	d := getDemand(rawID)
	was := d.local
	d.local = on
	if on && !was {
		ensureCapture(rawID)
		log.Printf("[state] local demand ON for %s", rawID)
	} else if !on && was {
		cleanupCapture(rawID)
		log.Printf("[state] local demand OFF for %s", rawID)
	}
	broadcastStatus()
}

func setGCDemand(rawID string, on bool) {
	d := getDemand(rawID)
	was := d.gc
	d.gc = on
	if on && !was {
		ensureCapture(rawID)
		log.Printf("[state] GC demand ON for %s", rawID)
	} else if !on && was {
		cleanupCapture(rawID)
		log.Printf("[state] GC demand OFF for %s", rawID)
	}
	broadcastStatus()
}

func isCaptureRunning(rawID string) bool {
	m := media.GetActiveIngests()
	return m[rawID]
}

// killOldProcesses aggressively terminates any previous sportshub.exe (except self),
// mediamtx.exe, and any processes holding our key ports (1935/8890/8000).
// This ensures a fresh run never sees "already listening" or long waits for ports
// that belonged to a prior (crashed or still-running) instance.
func killOldProcesses() {
	if runtime.GOOS != "windows" {
		return
	}
	log.Printf("[startup] Cleaning up previous sportshub/mediamtx instances and freeing ports...")
	ourPID := os.Getpid()

	// 1. Nuke mediamtx (and its children)
	exec.Command("taskkill", "/F", "/T", "/IM", "mediamtx.exe").Run()

	// 2. Kill other sportshub.exe processes (exclude our PID)
	psKill := fmt.Sprintf(`Get-Process -Name sportshub -ErrorAction SilentlyContinue | Where-Object { $_.Id -ne %d } | Stop-Process -Force -ErrorAction SilentlyContinue`, ourPID)
	exec.Command("powershell", "-NoProfile", "-Command", psKill).Run()

	// 3. Kill owners of the specific ports we need (TCP listeners + UDP endpoints)
	ports := []int{1935, 8890, 8000}
	for _, p := range ports {
		psTCP := fmt.Sprintf(`Get-NetTCPConnection -LocalPort %d -ErrorAction SilentlyContinue | Select-Object -ExpandProperty OwningProcess -Unique | Where-Object { $_ -ne %d } | Stop-Process -Force -ErrorAction SilentlyContinue`, p, ourPID)
		exec.Command("powershell", "-NoProfile", "-Command", psTCP).Run()

		psUDP := fmt.Sprintf(`Get-NetUDPEndpoint -LocalPort %d -ErrorAction SilentlyContinue | Select-Object -ExpandProperty OwningProcess -Unique | Where-Object { $_ -ne %d } | Stop-Process -Force -ErrorAction SilentlyContinue`, p, ourPID)
		exec.Command("powershell", "-NoProfile", "-Command", psUDP).Run()
	}

	time.Sleep(700 * time.Millisecond)
	log.Printf("[startup] Port/process cleanup complete (our pid=%d). 1935/8890 should be free for MediaMTX.", ourPID)
}

// getStateDir returns the same bin dir used for mediamtx/ffmpeg so we can store small state (last GC configs).
func getStateDir() (string, error) {
	appData, err := os.UserCacheDir()
	if err != nil {
		appData = os.TempDir()
	}
	dir := filepath.Join(appData, "sportshub", "bin")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func loadGCLastConfigs() {
	dir, err := getStateDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, "gc_lasts.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var m map[string]GCConfig
	if json.Unmarshal(b, &m) == nil {
		for k, v := range m {
			gcLastConfigs[k] = v
		}
		log.Printf("[gamechanger] loaded %d last GC config(s) from disk", len(m))
	}
}

func saveGCLastConfigs() {
	dir, err := getStateDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, "gc_lasts.json")
	b, _ := json.MarshalIndent(gcLastConfigs, "", "  ")
	_ = os.WriteFile(path, b, 0644)
}

// initPublicHost tries to find a usable LAN IP so phones on the same network
// can scan QR codes and reach the video viewer page.
func initPublicHost() {
	publicHost = "127.0.0.1"
	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || (iface.Flags&net.FlagLoopback) != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				publicHost = ip4.String()
				return // first suitable IPv4 is good enough for LAN use
			}
		}
	}
}

type ServerConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	RTMPPort int    `json:"rtmpPort"`
	HLSPort  int    `json:"hlsPort"`
}

func configHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ServerConfig{
		Host:     publicHost,
		Port:     8080,
		RTMPPort: 1935,
		HLSPort:  8888,
	})
}

func watchHandler(w http.ResponseWriter, r *http.Request) {
	// Expect /watch/cam0
	trimmed := strings.TrimPrefix(r.URL.Path, "/watch/")
	parts := strings.Split(trimmed, "/")
	streamPath := ""
	if len(parts) > 0 {
		streamPath = parts[0]
	}
	if streamPath == "" {
		http.NotFound(w, r)
		return
	}

	hlsURL := fmt.Sprintf("http://%s:8888/%s/index.m3u8", publicHost, streamPath)
	rtmpURL := fmt.Sprintf("rtmp://%s:1935/%s", publicHost, streamPath)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")

	page := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Sportshub • %s</title>
  <style>
    body { font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background:#0a0a0a; color:#eee; margin:0; padding:16px; display:flex; flex-direction:column; align-items:center; }
    h1 { font-size:1.1rem; margin:0 0 12px; }
    video { width:100%%; max-width:960px; background:#000; border-radius:12px; box-shadow:0 10px 30px rgba(0,0,0,0.6); }
    .meta { margin-top:12px; font-size:0.75rem; color:#666; word-break:break-all; text-align:center; }
    .btn { margin-top:12px; padding:10px 18px; background:#222; color:#ddd; border:1px solid #333; border-radius:8px; cursor:pointer; }
    .btn:hover { background:#333; }
  </style>
</head>
<body>
  <h1>Live • %s</h1>
  <video id="video" autoplay controls playsinline muted></video>
  <div class="meta">HLS stream: %s<br>RTMP (for OBS/VLC): %s</div>
  <button class="btn" onclick="const v=document.getElementById('video'); if(v.requestFullscreen) v.requestFullscreen(); else if(v.webkitRequestFullscreen) v.webkitRequestFullscreen();">Fullscreen</button>

  <script src="/static/hls.min.js"></script>
  <script>
    // Full HLS playback support using embedded hls.js for broad browser compatibility
    // (including non-Safari browsers on LAN without internet). Falls back to native HLS.
    const video = document.getElementById('video');
    const src = '%s';
    if (typeof Hls !== 'undefined' && Hls.isSupported()) {
      const hls = new Hls({ enableWorker: true, lowLatencyMode: true, backBufferLength: 30 });
      hls.loadSource(src);
      hls.attachMedia(video);
      hls.on(Hls.Events.MANIFEST_PARSED, function() {
        video.play().catch(function(){});
      });
      hls.on(Hls.Events.ERROR, function(event, data) {
        if (data.fatal) {
          console.error('HLS fatal error, falling back to native', data);
          video.src = src;
        }
      });
    } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
      video.src = src;
      video.addEventListener('loadedmetadata', function() {
        video.play().catch(function(){});
      });
    } else {
      video.src = src;
    }
  </script>
</body>
</html>`, streamPath, streamPath, hlsURL, rtmpURL, hlsURL)

	fmt.Fprint(w, page)
}

// ---------------- SSE realtime status hub ----------------

type eventHub struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

var hub = &eventHub{subs: make(map[chan string]struct{})}

func (h *eventHub) subscribe() chan string {
	ch := make(chan string, 8)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *eventHub) unsubscribe(ch chan string) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
	close(ch)
}

func (h *eventHub) broadcast(v any) {
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

// StatusSnapshot is the complete view pushed over SSE.
type StatusSnapshot struct {
	Ts      time.Time      `json:"ts"`
	Global  GlobalStatus   `json:"global"`
	Devices []DeviceStatus `json:"devices"`
}

type GlobalStatus struct {
	MediaMTXReady bool   `json:"mediaMTXReady"`
	ActiveIngests int    `json:"activeIngests"`
	GCActive      bool   `json:"gcActive"`
	GCPath        string `json:"gcPath,omitempty"`
	GCActiveRaw   string `json:"gcActiveRaw,omitempty"`
}

type DeviceStatus struct {
	RawID       string            `json:"rawId"`
	Name        string            `json:"name"`
	Path        string            `json:"path,omitempty"`
	LocalActive bool              `json:"localActive"`
	GCActive    bool              `json:"gcActive"`
	GCLast      *GCConfig         `json:"gcLast,omitempty"`
	Stats       *media.StreamStats `json:"stats,omitempty"`
	EgressStats *media.StreamStats `json:"egressStats,omitempty"`
}

var gcEgress media.StreamStats // egress stats for the active GC restream (protected by gcMu when written)

func broadcastStatus() {
	snap := buildStatusSnapshot()
	hub.broadcast(snap)
}

func buildStatusSnapshot() StatusSnapshot {
	snap := StatusSnapshot{Ts: time.Now()}

	activeIngests := media.GetActiveIngests()
	ingestStats := media.GetStreamStats()

	mu.Lock()
	gcMu.Lock()
	defer gcMu.Unlock()
	defer mu.Unlock()

	snap.Global.MediaMTXReady = media.IsMediaReady()
	snap.Global.ActiveIngests = len(activeIngests)
	snap.Global.GCActive = gcActive
	snap.Global.GCPath = gcActivePath

	seen := map[string]bool{}
	for path, info := range serverStreams {
		ds := DeviceStatus{
			RawID: info.RawID,
			Name:  info.Name,
			Path:  path,
		}
		dmd := getDemand(info.RawID)
		ds.LocalActive = dmd.local
		if path == gcActivePath {
			ds.GCActive = true
			snap.Global.GCActiveRaw = info.RawID
		}
		if last, ok := gcLastConfigs[info.RawID]; ok {
			ds.GCLast = &last
		}
		if st, ok := ingestStats[info.RawID]; ok && (st.FPS != 0 || st.Bitrate != "") {
			cp := st
			ds.Stats = &cp
		}
		if ds.GCActive && (gcEgress.FPS != 0 || gcEgress.Bitrate != "") {
			cp := gcEgress
			ds.EgressStats = &cp
		}
		snap.Devices = append(snap.Devices, ds)
		seen[info.RawID] = true
	}

	// Include remembered last-only devices (for "Restart GC (last)" on idle rows after restart of the exe)
	for raw, last := range gcLastConfigs {
		if seen[raw] {
			continue
		}
		ds := DeviceStatus{
			RawID:    raw,
			Name:     raw,
			GCActive: false,
			GCLast:   &last,
		}
		dmd := getDemand(raw)
		ds.LocalActive = dmd.local
		if st, ok := ingestStats[raw]; ok && (st.FPS != 0 || st.Bitrate != "") {
			cp := st
			ds.Stats = &cp
		}
		snap.Devices = append(snap.Devices, ds)
	}

	return snap
}

// eventsHandler is the SSE endpoint. Sends full snapshot on connect, then incremental updates.
func eventsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // helpful behind some proxies

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	log.Printf("[sse] client connected from %s", r.RemoteAddr)
	ch := hub.subscribe()
	defer hub.unsubscribe(ch)

	// Initial snapshot so the client is immediately up-to-date.
	snap := buildStatusSnapshot()
	b, _ := json.Marshal(snap)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if _, err := fmt.Fprint(w, msg); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func main() {
	killOldProcesses()
	loadGCLastConfigs()

	// Wire notifier from media layer (ingest start/stop + live fps/bitrate stats) into our SSE broadcaster.
	media.SetNotifier(func(event string, payload any) {
		if event == "ingest-started" || event == "ingest-stopped" || event == "stats" || event == "gc-stats" {
			go broadcastStatus()
		}
	})

	// Serve the dashboard (real file - we will embed later)
	http.Handle("/", http.FileServer(http.Dir("web/dist")))

	// API
	http.HandleFunc("/api/sources", sourcesHandler)
	http.HandleFunc("/api/stream/start", startStreamHandler)
	http.HandleFunc("/api/stream/stop", stopStreamHandler)
	http.HandleFunc("/api/status", statusHandler)
	http.HandleFunc("/api/qr", qrHandler)

	// GameChanger direct push
	http.HandleFunc("/api/gamechanger/start", gameChangerStartHandler)
	http.HandleFunc("/api/gamechanger/stop", gameChangerStopHandler)
	http.HandleFunc("/api/gamechanger/status", gameChangerStatusHandler)
	http.HandleFunc("/api/active-streams", activeStreamsHandler)

	// SSE realtime status (global + per device + live stats). UI connects once and stays fresh.
	http.HandleFunc("/api/events", eventsHandler)

	// Public config + viewer page so phones can scan QR codes and watch the live video
	// instead of trying to open raw RTMP URLs.
	http.HandleFunc("/api/config", configHandler)
	http.HandleFunc("/watch/", watchHandler)
	http.HandleFunc("/static/hls.min.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(hlsJS)
	})

	initPublicHost()

	port := ":8080"
	log.Printf("=== SportsHub Windows Spike ===")
	log.Printf("Open http://%s%s (use this address from phones on the same LAN) or http://localhost%s", publicHost, port, port)
	log.Printf("Browser Live Preview (top section) = browser getUserMedia")
	log.Printf("Server RTMP list (bottom) = what our ffmpeg can see for real RTMP streaming")

	// Pre-start MediaMTX early so it's ready when first ingest or GC is requested.
	// This avoids long waits/timeouts on first use.
	go func() {
		if err := media.InitMedia(); err != nil {
			log.Printf("[media] pre-init MediaMTX warning: %v", err)
		} else {
			// Push an initial status snapshot once MTX is up.
			go broadcastStatus()
		}
	}()

	// Light ticker so live stats (fps/bitrate) keep flowing to UI even during quiet periods of ffmpeg output,
	// and global view stays fresh while anything is streaming.
	go func() {
		t := time.NewTicker(1500 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			if len(media.GetActiveIngests()) > 0 || gcActive {
				broadcastStatus()
			}
		}
	}()

	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatal(err)
	}
}

func sourcesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	cams, err := sources.ListCameras()
	if err != nil || len(cams) == 0 {
		// Graceful fallback so the UI never goes completely dead
		cams = []sources.Camera{
			{ID: "video=Mevo Start", Name: "Mevo Start (Webcam Mode) [fallback]"},
		}
	}
	json.NewEncoder(w).Encode(cams)
}

type startReq struct {
	CameraID string `json:"cameraId"`
}

func startStreamHandler(w http.ResponseWriter, r *http.Request) {
	var req startReq
	_ = json.NewDecoder(r.Body).Decode(&req)

	mu.Lock()

	// Assign a clean, short stream path (cam0, cam1, ...)
	streamPath := fmt.Sprintf("cam%d", nextCamIndex)
	nextCamIndex++

	rtmpURL := fmt.Sprintf("rtmp://%s:1935/%s", publicHost, streamPath)

	streams[req.CameraID] = &Stream{
		CameraID:  req.CameraID,
		Active:    true,
		RTMP:      rtmpURL,
		StartedAt: time.Now().Format(time.RFC3339),
	}

	serverStreams[streamPath] = ServerStreamInfo{
		RawID: req.CameraID,
		Name:  req.CameraID,
		Path:  streamPath,
	}

	mu.Unlock()

	setLocalDemand(req.CameraID, true)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":     true,
		"stream": streams[req.CameraID],
	})
}

func stopStreamHandler(w http.ResponseWriter, r *http.Request) {
	var req startReq
	_ = json.NewDecoder(r.Body).Decode(&req)

	mu.Lock()
	delete(streams, req.CameraID)

	// Find and remove from serverStreams if present
	stoppedPath := ""
	for path, info := range serverStreams {
		if info.RawID == req.CameraID {
			stoppedPath = path
			delete(serverStreams, path)
			break
		}
	}
	mu.Unlock()

	setLocalDemand(req.CameraID, false)

	// If this path was the one sending to GC, stop the GC push too
	if stoppedPath != "" && stoppedPath == gcActivePath {
		gcMu.Lock()
		if gcCmd != nil {
			_ = gcCmd.Process.Kill()
			gcCmd = nil
		}
		gcActivePath = ""
		gcActive = false
		gcCamera = ""
		gcMu.Unlock()
		log.Printf("[gamechanger] Stopped because local stream %s was stopped", stoppedPath)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"mode":    "windows-spike",
		"version": "0.0.1-dev",
		"os":      runtime.GOOS,
	})
}

// qrHandler serves a PNG QR code for a given text (used for RTMP URLs)
func qrHandler(w http.ResponseWriter, r *http.Request) {
	text := r.URL.Query().Get("text")
	if text == "" {
		http.Error(w, "missing text", http.StatusBadRequest)
		return
	}

	png, err := qrcode.Encode(text, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, "failed to generate qr", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(png)
}

func sanitizeID(s string) string {
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "=", "_")
	s = strings.ReplaceAll(s, ":", "_")
	return strings.ToLower(s)
}

// ==================== GameChanger Direct Push ====================

type gameChangerStartReq struct {
	CameraPath string `json:"cameraPath"` // e.g. "cam0"
	GcServer   string `json:"gcServer"`   // e.g. rtmp://ingest.gamechanger.io/live   (used only if GcFullUrl is empty)
	GcKey      string `json:"gcKey"`      // the stream key from GameChanger app     (used only if GcFullUrl is empty)
	GcFullUrl  string `json:"gcFullUrl"`  // full RTMP URL (takes precedence over server+key)
}

func gameChangerStartHandler(w http.ResponseWriter, r *http.Request) {
	var req gameChangerStartReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[gamechanger] Bad JSON in start request: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	log.Printf("[gamechanger] Start request received: cameraPath=%s, hasFullUrl=%v, gcServer=%s, gcKeyLen=%d",
		req.CameraPath, req.GcFullUrl != "", req.GcServer, len(req.GcKey))

	if req.CameraPath == "" {
		log.Printf("[gamechanger] 400: cameraPath is required. received: %+v", req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "cameraPath is required"})
		return
	}

	hasFull := strings.TrimSpace(req.GcFullUrl) != ""
	hasSeparate := strings.TrimSpace(req.GcServer) != "" && strings.TrimSpace(req.GcKey) != ""

	if !hasFull && !hasSeparate {
		log.Printf("[gamechanger] 400: no fullUrl and no (server+key). received full=%q server=%q keyLen=%d cameraPath=%q", req.GcFullUrl, req.GcServer, len(req.GcKey), req.CameraPath)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Either gcFullUrl or both gcServer + gcKey must be provided"})
		return
	}

	// Find rawID: if cameraPath is a clean path (e.g. "cam0" from old last), lookup its raw.
	// Also accept raw directly (from "Use for GameChanger" or restart last sending raw).
	rawID := req.CameraPath
	mu.Lock()
	for p, info := range serverStreams {
		if p == req.CameraPath || info.RawID == req.CameraPath {
			rawID = info.RawID
			break
		}
	}
	available := []string{}
	for k := range serverStreams {
		available = append(available, k)
	}
	mu.Unlock()

	setGCDemand(rawID, true)
	path := ensureCapture(rawID)
	if path == "" {
		path = req.CameraPath
	}

	// Wait for the local ingest to actually be running and publishing before starting the pull.
	// This avoids the race where the GC restream tries to connect before the publisher is up.
	log.Printf("[gamechanger] waiting for local ingest for %s to be active...", rawID)
	for i := 0; i < 60; i++ { // up to 30s
		if media.GetActiveIngests()[rawID] {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Construct destination.
	// Full URL takes precedence (GameChanger often gives you one complete URL).
	var dest string
	if req.GcFullUrl != "" {
		dest = req.GcFullUrl
	} else {
		dest = strings.TrimRight(req.GcServer, "/") + "/" + strings.TrimLeft(req.GcKey, "/")
	}

	// Build a GameChanger-friendly ffmpeg command.
	// Pull clean feed from local MediaMTX (now 1080p30 from ingest) and re-encode with settings that GameChanger wants.
	// Force 1080p output + 6Mbps target (GameChanger prefers 1080p for the app).
	source := fmt.Sprintf("srt://127.0.0.1:8890?streamid=read:%s&latency=100000&mode=caller", path)

	args := []string{
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		// These must be high enough for ffmpeg to parse H.264 SPS/PPS + resolution from
		// the mpegts over SRT. Too low (the old 32/0) causes "unspecified size" / "not enough frames"
		// and the video stream gets dropped — only audio reaches GameChanger.
		"-probesize", "500000",
		"-analyzeduration", "1000000",
		"-err_detect", "ignore_err",
		"-i", source,
		"-vf", "scale=1920:1080",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-profile:v", "high",
		"-level", "4.1",
		"-b:v", "6000k",
		"-maxrate", "7000k",
		"-bufsize", "9000k",
		"-g", "30",           // lower GOP for lower latency (1s at 30fps); GC accepts it
		"-keyint_min", "30",
		"-sc_threshold", "0",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-b:a", "128k",
		"-ar", "48000",
		"-f", "flv",
		dest,
	}

	ffmpegPath, err := media.GetFFmpegPath()
	if err != nil {
		log.Printf("[gamechanger] failed to get ffmpeg path: %v", err)
		http.Error(w, "failed to locate ffmpeg binary", http.StatusInternalServerError)
		return
	}

	log.Printf("[gamechanger] Starting ffmpeg pull from %s → %s", source, dest)
	log.Printf("[gamechanger] ffmpeg %v", args)

	cmd := exec.Command(ffmpegPath, args...)
	cmd.Stdout = nil

	// Capture stderr for diagnostics + live egress stats (to GC)
	stderrPipe, _ := cmd.StderrPipe()
	go func() {
		if stderrPipe != nil {
			buf := make([]byte, 4096)
			for {
				n, err := stderrPipe.Read(buf)
				if n > 0 {
					s := string(buf[:n])
					log.Printf("[gamechanger ffmpeg stderr] %s", s)

					// Detect when GameChanger app closes the stream (RTMP push side fails)
					// This lets us immediately clear GC state even if the process exit monitor is slow.
					lowerS := strings.ToLower(s)
					if (strings.Contains(lowerS, "connection to") && strings.Contains(lowerS, "failed")) ||
						strings.Contains(lowerS, "error writing") ||
						strings.Contains(lowerS, "immediate exit requested") ||
						(strings.Contains(lowerS, "av_interleaved_write_frame") && strings.Contains(lowerS, "end of file")) {
						log.Printf("[gamechanger] detected remote close/error from GameChanger side in restream stderr, forcing state cleanup")
						go func() {
							gcMu.Lock()
							if gcActive {
								c := gcCmd
								gcCmd = nil
								gcActive = false
								p := gcActivePath
								gcActivePath = ""
								gcCamera = ""
								gcEgress = media.StreamStats{}
								gcMu.Unlock()

								if c != nil && c.Process != nil {
									_ = c.Process.Kill()
								}

								raw := ""
								mu.Lock()
								for pp, info := range serverStreams {
									if pp == p {
										raw = info.RawID
										break
									}
								}
								mu.Unlock()

								if raw != "" {
									setGCDemand(raw, false)
								}
								broadcastStatus()
							} else {
								gcMu.Unlock()
							}
						}()
					}

					fps, br, spd, fr := media.ParseFFmpegProgressLine(s)
					if fps != 0 || br != "" || fr != 0 {
						gcMu.Lock()
						if fps != 0 {
							gcEgress.FPS = fps
						}
						if br != "" {
							gcEgress.Bitrate = br
						}
						if spd != "" {
							gcEgress.Speed = spd
						}
						if fr != 0 {
							gcEgress.Frames = fr
						}
						gcMu.Unlock()
						broadcastStatus()
					}
				}
				if err != nil {
					break
				}
			}
		}
	}()

	if err := cmd.Start(); err != nil {
		log.Printf("[gamechanger] failed to start ffmpeg: %v", err)
		http.Error(w, fmt.Sprintf("failed to start GameChanger push: %v", err), http.StatusInternalServerError)
		return
	}

	// Claim the GC slot under lock, but hold the lock only briefly.
	// Do heavy work (monitor goroutine, lastconfig save, broadcast) after releasing the lock
	// to avoid blocking other gcMu users (status, stop, ticker, stderr stats updater, etc.)
	// and to ensure the HTTP response is sent promptly so the client UI can update from "Starting...".
	gcMu.Lock()
	if gcActive {
		gcMu.Unlock()
		_ = cmd.Process.Kill()
		http.Error(w, "GameChanger stream already active", http.StatusConflict)
		return
	}
	gcCmd = cmd
	gcActive = true
	gcActivePath = path
	gcCamera = req.CameraPath
	gcMu.Unlock()

	// Monitor the restream process so we detect when GameChanger app (or remote)
	// closes the connection. When that happens we must clear gc state, drop demand
	// (so capture can stop if nothing else needs it), and push SSE update to UI.
	go func(c *exec.Cmd, cleanPath, rID string) {
		waitErr := c.Wait()
		gcMu.Lock()
		if gcCmd == c {
			log.Printf("[gamechanger] restream process exited (GameChanger app stopped the stream or connection closed): %v", waitErr)
			gcCmd = nil
			gcActive = false
			gcActivePath = ""
			gcCamera = ""
			gcEgress = media.StreamStats{}
			gcMu.Unlock()

			if rID != "" {
				setGCDemand(rID, false)
			}
			broadcastStatus()
		} else {
			gcMu.Unlock()
		}
	}(cmd, path, rawID)

	// store last config *by the stable raw device ID* so restart works across sportshub restarts
	// and we can show "Restart GC (last)" for a device even if it has no current local stream.
	gcLastConfigs[rawID] = GCConfig{
		FullURL: req.GcFullUrl,
		Server:  req.GcServer,
		Key:     req.GcKey,
		RawID:   rawID,
	}
	saveGCLastConfigs()

	log.Printf("[gamechanger] Started push for %s (clean=%s) → %s (fullUrl=%v)", req.CameraPath, path, dest, req.GcFullUrl != "")

	// zero previous egress stats for the new push (brief lock)
	gcMu.Lock()
	gcEgress = media.StreamStats{}
	gcMu.Unlock()

	broadcastStatus()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"camera":  req.CameraPath,
		"dest":    dest,
	})
}

func gameChangerStopHandler(w http.ResponseWriter, r *http.Request) {
	gcMu.Lock()

	if !gcActive || gcCmd == nil {
		gcMu.Unlock()
		http.Error(w, "no active GameChanger stream", http.StatusConflict)
		return
	}

	log.Printf("[gamechanger] Stopping stream for %s", gcCamera)

	c := gcCmd
	activePath := gcActivePath
	gcCmd = nil
	gcActive = false
	gcActivePath = ""
	gcCamera = ""
	gcEgress = media.StreamStats{}
	gcMu.Unlock()

	// Do demand cleanup and broadcast outside the lock to avoid deadlock
	// (setGCDemand and broadcastStatus take other locks including gcMu).
	raw := ""
	mu.Lock()
	for p, info := range serverStreams {
		if p == activePath {
			raw = info.RawID
			break
		}
	}
	mu.Unlock()
	if raw != "" {
		setGCDemand(raw, false)
	}

	if c != nil && c.Process != nil {
		_ = c.Process.Kill()
	}

	broadcastStatus()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func gameChangerStatusHandler(w http.ResponseWriter, r *http.Request) {
	gcMu.Lock()
	defer gcMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"active": gcActive,
		"camera": gcCamera,
		"path":   gcActivePath,
	})
}

func activeStreamsHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	gcMu.Lock()
	defer gcMu.Unlock()
	defer mu.Unlock()

	result := []ServerStreamInfo{}
	seenRaws := map[string]bool{}
	for path, info := range serverStreams {
		as := ServerStreamInfo{
			RawID: info.RawID,
			Name:  info.Name,
			Path:  path,
		}
		if path == gcActivePath {
			as.GCActive = true
		}
		if last, ok := gcLastConfigs[info.RawID]; ok {
			lastCopy := last
			as.GCLast = &lastCopy
		}
		result = append(result, as)
		seenRaws[info.RawID] = true
	}

	// Also surface remembered last configs for raw devices that have no current serverStream entry
	// (e.g. after a full STOP that cleaned the local path, or after sportshub.exe restart).
	// This lets the UI render a "Restart GC (last)" button on the raw device row.
	for raw, last := range gcLastConfigs {
		if seenRaws[raw] {
			continue
		}
		as := ServerStreamInfo{
			RawID:    raw,
			Name:     raw,
			Path:     "",
			GCActive: false,
			GCLast:   &last,
		}
		result = append(result, as)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
