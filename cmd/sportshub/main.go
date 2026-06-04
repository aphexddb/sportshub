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

var (
	// camMgr owns every camera's lifecycle state machine (capture + GameChanger).
	camMgr *CameraManager

	// gcQuality is the global broadcast quality for GameChanger pushes:
	// "1080p" (default) or "720p". Set via the GC start request / quality endpoint.
	gcQuality   = "1080p"
	gcQualityMu sync.Mutex
)

// gcEncodeParams holds the GameChanger-recommended encoder settings for a quality.
type gcEncodeParams struct {
	scale   string // ffmpeg scale filter target, e.g. "1920:1080"
	bv      string // target video bitrate
	maxrate string
	bufsize string
	level   string // H.264 level
}

// gcParamsForQuality returns GameChanger best-practice encode settings for the
// requested quality. Anything other than "720p" falls back to 1080p.
func gcParamsForQuality(q string) gcEncodeParams {
	// GameChanger ingests RTMP and redelivers as HLS (~10-20s viewer latency), so it
	// expects a stable, near-CBR feed. We set maxrate == bv and bufsize == bv (1s VBV)
	// to mimic OBS "CBR", which GameChanger's own OBS guide recommends.
	switch normalizeQuality(q) {
	case "480p":
		// 480p30: ~1500 kbps, level 3.0. For weak field upload (~4 Mbps up) where GC
		// advises a stable low quality over a stuttering high one.
		return gcEncodeParams{scale: "854:480", bv: "1500k", maxrate: "1500k", bufsize: "1500k", level: "3.0"}
	case "720p":
		// 720p30: ~3500 kbps (GoPro GC default is 2500; 3500 gives motion headroom), level 3.1.
		return gcEncodeParams{scale: "1280:720", bv: "3500k", maxrate: "3500k", bufsize: "3500k", level: "3.1"}
	}
	// Default 1080p30: ~6000 kbps (top of GC's recommended range), level 4.1.
	return gcEncodeParams{scale: "1920:1080", bv: "6000k", maxrate: "6000k", bufsize: "6000k", level: "4.1"}
}

// normalizeQuality coerces user input to a supported value ("1080p" or "720p").
func normalizeQuality(q string) string {
	s := strings.ToLower(strings.TrimSpace(q))
	if strings.Contains(s, "480") {
		return "480p"
	}
	if strings.Contains(s, "720") {
		return "720p"
	}
	return "1080p"
}

var publicHost string // non-loopback IP for phone/LAN access (used for viewer URLs and QR codes)

type GCConfig struct {
	FullURL string `json:"fullUrl,omitempty"`
	Server  string `json:"server,omitempty"`
	Key     string `json:"key,omitempty"`
	RawID   string `json:"rawId,omitempty"`
}

type ServerStreamInfo struct {
	RawID    string    `json:"rawId"`
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	GCActive bool      `json:"gcActive"`
	GCLast   *GCConfig `json:"gcLast,omitempty"`
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
    .status { font-size:0.8rem; color:#0f0; margin:8px 0; }
  </style>
</head>
<body>
  <h1>Live • %s</h1>
  <div id="status" class="status">Connecting (WebRTC for low latency)...</div>
  <video id="video" autoplay controls playsinline muted></video>
  <div class="meta">WebRTC (low-latency): port 8889<br>HLS (higher latency): %s<br>RTMP (for OBS/VLC): %s</div>
  <button class="btn" onclick="const v=document.getElementById('video'); if(v.requestFullscreen) v.requestFullscreen(); else if(v.webkitRequestFullscreen) v.webkitRequestFullscreen();">Fullscreen</button>

  <script src="/static/hls.min.js"></script>
  <script>
    const video = document.getElementById('video');
    const statusEl = document.getElementById('status');
    const path = '%s';
    const webrtcUrl = 'http://%s:8889/' + path + '/whep';
    const hlsUrl = '%s';

    async function startWebRTC() {
      try {
        const pc = new RTCPeerConnection();
        pc.ontrack = (event) => {
          if (event.streams && event.streams[0]) {
            video.srcObject = event.streams[0];
            statusEl.textContent = 'Playing via WebRTC (low latency)';
            video.play().catch(() => {});
          }
        };
        pc.onconnectionstatechange = () => {
          statusEl.textContent = 'WebRTC state: ' + pc.connectionState;
        };

        const offer = await pc.createOffer({
          offerToReceiveVideo: true,
          offerToReceiveAudio: true
        });
        await pc.setLocalDescription(offer);

        const res = await fetch(webrtcUrl, {
          method: 'POST',
          headers: { 'Content-Type': 'application/sdp' },
          body: offer.sdp
        });

        if (!res.ok) throw new Error('WHEP failed: ' + res.status);

        const answerSdp = await res.text();
        await pc.setRemoteDescription({ type: 'answer', sdp: answerSdp });
      } catch (err) {
        console.error('WebRTC failed, falling back to HLS:', err);
        statusEl.textContent = 'WebRTC failed, using HLS...';
        startHLSFallback();
      }
    }

    function startHLSFallback() {
      if (typeof Hls !== 'undefined' && Hls.isSupported()) {
        const hls = new Hls({ enableWorker: true, lowLatencyMode: true, backBufferLength: 8, maxBufferLength: 12 });
        hls.loadSource(hlsUrl);
        hls.attachMedia(video);
        hls.on(Hls.Events.MANIFEST_PARSED, () => { video.play().catch(()=>{}); });
      } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
        video.src = hlsUrl;
        video.addEventListener('loadedmetadata', () => { video.play().catch(()=>{}); });
      } else {
        video.src = hlsUrl;
      }
    }

    // Prefer WebRTC for ~1s latency on local LAN
    startWebRTC();
  </script>
</body>
</html>`, streamPath, streamPath, hlsURL, rtmpURL, streamPath, publicHost, hlsURL)

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
	GCQuality     string `json:"gcQuality"` // global broadcast quality: "1080p" or "720p"
}

type DeviceStatus struct {
	RawID       string             `json:"rawId"`
	Name        string             `json:"name"`
	Path        string             `json:"path,omitempty"`
	State       string             `json:"state"`           // camera state machine: idle/starting/live/stopping/error
	GCPhase     string             `json:"gcPhase"`         // GC sub-state: idle/starting/streaming/error
	Error       string             `json:"error,omitempty"` // last error message, if State/GCPhase is error
	LocalActive bool               `json:"localActive"`
	GCActive    bool               `json:"gcActive"`
	GCLast      *GCConfig          `json:"gcLast,omitempty"`
	Stats       *media.StreamStats `json:"stats,omitempty"`
	EgressStats *media.StreamStats `json:"egressStats,omitempty"`
}

func broadcastStatus() {
	snap := buildStatusSnapshot()
	hub.broadcast(snap)
}

func buildStatusSnapshot() StatusSnapshot {
	snap := StatusSnapshot{Ts: time.Now()}
	// Always emit a (possibly empty) array, never null — otherwise the client's
	// Array.isArray(devices) guard rejects the update and the UI shows stale state
	// when the last device is stopped.
	snap.Devices = []DeviceStatus{}

	snap.Global.MediaMTXReady = media.IsMediaReady()
	snap.Global.ActiveIngests = len(media.GetActiveIngests())
	gcQualityMu.Lock()
	snap.Global.GCQuality = gcQuality
	gcQualityMu.Unlock()

	if camMgr != nil {
		devs, g := camMgr.Snapshot()
		snap.Devices = devs
		snap.Global.GCActive = g.Active
		snap.Global.GCPath = g.Path
		snap.Global.GCActiveRaw = g.RawID
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

	// Per-camera state machines; broadcast SSE on any transition.
	camMgr = NewCameraManager(func() { go broadcastStatus() })

	// Wire notifier from media layer (ingest start/stop + live fps/bitrate stats) into our SSE broadcaster.
	// An unexpected ingest exit (camera unplugged, ffmpeg crash) is reported to the state machine so
	// the affected camera transitions to Error instead of silently looking "live".
	media.SetNotifier(func(event string, payload any) {
		if event == "ingest-stopped" {
			if pm, ok := payload.(map[string]any); ok {
				if raw, ok := pm["rawId"].(string); ok && camMgr != nil {
					camMgr.onIngestStopped(raw)
				}
			}
		}
		if event == "ingest-started" || event == "ingest-stopped" || event == "stats" || event == "gc-stats" {
			go broadcastStatus()
		}
	})

	// Serve the dashboard (real file - we will embed later).
	// Disable caching so UI iterations show up immediately on phones/browsers
	// instead of serving a stale index.html.
	dashboard := http.FileServer(http.Dir("web/dist"))
	http.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		dashboard.ServeHTTP(w, r)
	}))

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
	http.HandleFunc("/api/gamechanger/quality", gameChangerQualityHandler)
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
			if len(media.GetActiveIngests()) > 0 {
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
	if req.CameraID == "" {
		http.Error(w, "cameraId required", http.StatusBadRequest)
		return
	}

	camMgr.StartLocal(req.CameraID, req.CameraID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func stopStreamHandler(w http.ResponseWriter, r *http.Request) {
	var req startReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.CameraID == "" {
		http.Error(w, "cameraId required", http.StatusBadRequest)
		return
	}

	// Stop tears down both the GameChanger push (if any) and the local capture for this
	// device, transitioning it back to Idle and broadcasting the new state.
	camMgr.Stop(req.CameraID)

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
	Quality    string `json:"quality"`    // broadcast quality: "1080p" (default) or "720p"
}

// mediaMTXPathReady reports whether MediaMTX has a live publisher with parsed tracks on
// the given path, plus a short human-readable reason for logging. This is the authoritative
// signal that an SRT reader can connect without being rejected with "no one is publishing":
// neither "process running" nor "frames encoded" guarantee MediaMTX has registered the
// publisher and parsed its tracks yet.
//
// We query /v3/paths/list (not /v3/paths/get/<path>) on purpose: the "get" endpoint logs a
// noisy "ERR [API] path not found" on every miss while we poll during startup. The "list"
// endpoint returns all paths without erroring on a not-yet-existing one.
func mediaMTXPathReady(path string) (bool, string) {
	client := http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get("http://127.0.0.1:9997/v3/paths/list")
	if err != nil {
		return false, fmt.Sprintf("API unreachable: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("API status %d", resp.StatusCode)
	}
	var list struct {
		Items []struct {
			Name   string   `json:"name"`
			Ready  bool     `json:"ready"`
			Tracks []string `json:"tracks"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return false, fmt.Sprintf("API decode error: %v", err)
	}
	for _, it := range list.Items {
		if it.Name != path {
			continue
		}
		if it.Ready && len(it.Tracks) > 0 {
			return true, fmt.Sprintf("ready, tracks=%v", it.Tracks)
		}
		return false, "publisher connecting (tracks not parsed yet)"
	}
	return false, "no publisher on path yet"
}

// waitForPathReady polls mediaMTXPathReady until the path is ready or timeout elapses,
// logging the first attempt, any change in reason, and the final outcome so the startup
// handshake is visible in the logs.
func waitForPathReady(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	start := time.Now()
	attempts := 0
	lastReason := ""
	for time.Now().Before(deadline) {
		attempts++
		ready, reason := mediaMTXPathReady(path)
		if ready {
			log.Printf("[ready] path %s ready after %v (%d checks): %s", path, time.Since(start).Round(time.Millisecond), attempts, reason)
			return true
		}
		if reason != lastReason {
			log.Printf("[ready] path %s waiting: %s", path, reason)
			lastReason = reason
		}
		time.Sleep(250 * time.Millisecond)
	}
	log.Printf("[ready] path %s NOT ready after %v (%d checks); last: %s", path, time.Since(start).Round(time.Millisecond), attempts, lastReason)
	return false
}

func gameChangerStartHandler(w http.ResponseWriter, r *http.Request) {
	var req gameChangerStartReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[gamechanger] Bad JSON in start request: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	quality := normalizeQuality(req.Quality)
	gcQualityMu.Lock()
	gcQuality = quality
	gcQualityMu.Unlock()

	log.Printf("[gamechanger] Start request received: cameraPath=%s, hasFullUrl=%v, gcServer=%s, gcKeyLen=%d, quality=%s",
		req.CameraPath, req.GcFullUrl != "", req.GcServer, len(req.GcKey), quality)

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

	// Resolve the raw device id (cameraPath may be a clean path like "cam0" or a raw id).
	rawID := camMgr.resolve(req.CameraPath)

	// Destination: full URL wins; otherwise server + key.
	var dest string
	if req.GcFullUrl != "" {
		dest = req.GcFullUrl
	} else {
		dest = strings.TrimRight(req.GcServer, "/") + "/" + strings.TrimLeft(req.GcKey, "/")
	}

	cfg := GCConfig{FullURL: req.GcFullUrl, Server: req.GcServer, Key: req.GcKey, RawID: rawID}

	// Hand off to the state machine. It starts capture if needed and only launches the GC
	// pull once the camera reaches StateLive (publisher confirmed ready by MediaMTX), so the
	// "no one is publishing" race is impossible. Returns quickly; the UI tracks progress via SSE.
	if err := camMgr.StartGC(rawID, req.CameraPath, dest, cfg); err != nil {
		log.Printf("[gamechanger] start rejected: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	log.Printf("[gamechanger] start accepted for %s (raw=%s) -> %s (fullUrl=%v, quality=%s)", req.CameraPath, rawID, dest, req.GcFullUrl != "", quality)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":     true,
		"camera": req.CameraPath,
		"dest":   dest,
	})
}

func gameChangerStopHandler(w http.ResponseWriter, r *http.Request) {
	// Stops whichever camera is currently pushing to GameChanger (single-GC model).
	// Local capture keeps running if it was independently requested.
	camMgr.StopActiveGC()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func gameChangerStatusHandler(w http.ResponseWriter, r *http.Request) {
	_, g := camMgr.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"active": g.Active,
		"path":   g.Path,
		"camera": g.RawID,
	})
}

// gameChangerQualityHandler sets the global broadcast quality ("1080p" or "720p").
// This is a global server config change: it updates state and broadcasts to all
// connected clients via SSE so every viewer's UI stays in sync.
func gameChangerQualityHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Quality string `json:"quality"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	quality := normalizeQuality(req.Quality)
	gcQualityMu.Lock()
	gcQuality = quality
	gcQualityMu.Unlock()

	log.Printf("[gamechanger] global quality set to %s", quality)

	// Push the new global state to all clients.
	broadcastStatus()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"quality": quality})
}

func activeStreamsHandler(w http.ResponseWriter, r *http.Request) {
	// Same per-device view the SSE snapshot uses, sourced from the state machine.
	devs, _ := camMgr.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(devs)
}
