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

	// serverStreams: clean path (e.g. "cam0") -> raw cameraID
	serverStreams = make(map[string]string)
	nextCamIndex  int

	// GameChanger state (only one active at a time for now)
	gameChangerMu     sync.Mutex
	gameChangerActive bool
	gameChangerCmd    *exec.Cmd
	gameChangerCamera string // e.g. "cam0"
)

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
	serverStreams[streamPath] = req.CameraID

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
	for path, camID := range serverStreams {
		if camID == req.CameraID {
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
	GcServer   string `json:"gcServer"`   // e.g. rtmp://ingest.gamechanger.io/live
	GcKey      string `json:"gcKey"`      // the stream key from GameChanger app
}

func gameChangerStartHandler(w http.ResponseWriter, r *http.Request) {
	var req gameChangerStartReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if req.CameraPath == "" || req.GcServer == "" || req.GcKey == "" {
		http.Error(w, "cameraPath, gcServer and gcKey are required", http.StatusBadRequest)
		return
	}

	gameChangerMu.Lock()
	defer gameChangerMu.Unlock()

	if gameChangerActive {
		http.Error(w, "GameChanger stream already active", http.StatusConflict)
		return
	}

	// Construct destination. GameChanger usually wants server + "/" + key
	dest := strings.TrimRight(req.GcServer, "/") + "/" + strings.TrimLeft(req.GcKey, "/")

	// Build a GameChanger-friendly ffmpeg command.
	// We pull from our local MediaMTX clean feed (e.g. cam0) and push to GameChanger.
	// The frontend should pass a clean local path like "cam0".
	source := fmt.Sprintf("rtmp://127.0.0.1:1935/%s", req.CameraPath)

	args := []string{
		"-i", source,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-b:v", "4500k",
		"-maxrate", "5000k",
		"-bufsize", "6000k",
		"-g", "60", // 2 seconds at 30fps
		"-keyint_min", "60",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-b:a", "128k",
		"-f", "flv",
		dest,
	}

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = nil
	cmd.Stderr = nil // in production we would capture this

	if err := cmd.Start(); err != nil {
		http.Error(w, fmt.Sprintf("failed to start GameChanger push: %v", err), http.StatusInternalServerError)
		return
	}

	gameChangerCmd = cmd
	gameChangerActive = true
	gameChangerCamera = req.CameraPath

	log.Printf("[gamechanger] Started push for %s → %s", req.CameraPath, dest)

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
