package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

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
)

func main() {
	// Serve the dashboard (real file - we will embed later)
	http.Handle("/", http.FileServer(http.Dir("web/dist")))

	// API
	http.HandleFunc("/api/sources", sourcesHandler)
	http.HandleFunc("/api/stream/start", startStreamHandler)
	http.HandleFunc("/api/stream/stop", stopStreamHandler)
	http.HandleFunc("/api/status", statusHandler)

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
			log.Printf("[server] Ingest failed for device %q → %s: %v", req.CameraID, streamPath, err)
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

func sanitizeID(s string) string {
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "=", "_")
	s = strings.ReplaceAll(s, ":", "_")
	return strings.ToLower(s)
}
