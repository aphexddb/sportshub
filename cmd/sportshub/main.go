package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"sportshub2/pkg/media"
	"sportshub2/pkg/sources"
)

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
	gameChangerMu     sync.Mutex
	gameChangerActive bool
	gameChangerCmd    *exec.Cmd
	gameChangerCamera string // e.g. "cam0"
)

type ServerStreamInfo struct {
	RawID   string `json:"rawId"`
	Name    string `json:"name"`
	Path    string `json:"path"`
}

func main() {
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

	port := ":8080"
	log.Printf("=== SportsHub Windows Spike ===")
	log.Printf("Open http://localhost%s", port)
	log.Printf("Browser Live Preview (top section) = browser getUserMedia")
	log.Printf("Server RTMP list (bottom) = what our ffmpeg can see for real RTMP streaming")

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

	rtmpURL := fmt.Sprintf("rtmp://127.0.0.1:1935/%s", streamPath)

	streams[req.CameraID] = &Stream{
		CameraID:  req.CameraID,
		Active:    true,
		RTMP:      rtmpURL,
		StartedAt: time.Now().Format(time.RFC3339),
	}

	serverStreams[streamPath] = ServerStreamInfo{
		RawID: req.CameraID,
		Name:  req.CameraID, // TODO: could store friendly name if we had it
		Path:  streamPath,
	}

	mu.Unlock()

	// Start the real ingest in background (MediaMTX + ffmpeg dshow → RTMP)
	go func() {
		if err := media.StartIngestForCamera(req.CameraID, streamPath); err != nil {
			log.Printf("[server] ============================================")
			log.Printf("[server] Ingest FAILED for device %q → %s", req.CameraID, streamPath)
			log.Printf("[server] Error: %v", err)
			log.Printf("[server] Common causes: MediaMTX not ready, device in use by browser, wrong resolution for camera")
			log.Printf("[server] ============================================")
		}
	}()

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
	for path, info := range serverStreams {
		if info.RawID == req.CameraID {
			delete(serverStreams, path)
			break
		}
	}
	mu.Unlock()

	media.StopIngest(req.CameraID)

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

	// Validate that this clean path is currently being ingested locally
	mu.Lock()
	_, isActive := serverStreams[req.CameraPath]
	available := []string{}
	for k := range serverStreams {
		available = append(available, k)
	}
	mu.Unlock()

	if !isActive {
		log.Printf("[gamechanger] 400: no active local stream for cameraPath=%q. Available clean paths: %v", req.CameraPath, available)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("No active local stream for path %q. Start the local RTMP stream first (using the Server Cameras section). Available: %v", req.CameraPath, available),
		})
		return
	}

	gameChangerMu.Lock()
	defer gameChangerMu.Unlock()

	if gameChangerActive {
		http.Error(w, "GameChanger stream already active", http.StatusConflict)
		return
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
	// Pull clean feed from local MediaMTX and re-encode with settings that GameChanger likes.
	// 720p30 @ ~5Mbps is very reliable for GC. Use 1080p if you have excellent upload.
	source := fmt.Sprintf("rtmp://127.0.0.1:1935/%s", req.CameraPath)

	args := []string{
		"-i", source,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-profile:v", "high",
		"-level", "4.1",
		"-b:v", "5000k",
		"-maxrate", "5500k",
		"-bufsize", "7000k",
		"-g", "60",           // 2 second keyframes at 30fps (GameChanger likes this)
		"-keyint_min", "60",
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

	// Capture stderr for diagnostics, similar to local ingest
	stderrPipe, _ := cmd.StderrPipe()
	go func() {
		if stderrPipe != nil {
			buf := make([]byte, 4096)
			for {
				n, err := stderrPipe.Read(buf)
				if n > 0 {
					log.Printf("[gamechanger ffmpeg stderr] %s", string(buf[:n]))
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

	gameChangerCmd = cmd
	gameChangerActive = true
	gameChangerCamera = req.CameraPath

	log.Printf("[gamechanger] Started push for %s → %s (fullUrl=%v)", req.CameraPath, dest, req.GcFullUrl != "")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"camera":  req.CameraPath,
		"dest":    dest,
	})
}

func gameChangerStopHandler(w http.ResponseWriter, r *http.Request) {
	gameChangerMu.Lock()
	defer gameChangerMu.Unlock()

	if !gameChangerActive || gameChangerCmd == nil {
		http.Error(w, "no active GameChanger stream", http.StatusConflict)
		return
	}

	log.Printf("[gamechanger] Stopping stream for %s", gameChangerCamera)

	_ = gameChangerCmd.Process.Kill()
	gameChangerCmd = nil
	gameChangerActive = false
	gameChangerCamera = ""

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func gameChangerStatusHandler(w http.ResponseWriter, r *http.Request) {
	gameChangerMu.Lock()
	defer gameChangerMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"active": gameChangerActive,
		"camera": gameChangerCamera,
	})
}

func activeStreamsHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	result := []ServerStreamInfo{}
	for _, info := range serverStreams {
		result = append(result, info)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
