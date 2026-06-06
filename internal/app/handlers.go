package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"

	"sportshub/internal/encode"
	"sportshub/internal/sources"
	"sportshub/internal/status"
	"sportshub/internal/wifi"
)

// httpPort is the numeric port the dashboard/API listens on, parsed from cfg.Port (e.g.
// ":80" -> 80). The web UI uses it (via /api/config) to build /watch links, so it must
// reflect the real port, not a hardcoded value.
func (a *App) httpPort() int {
	if n, err := strconv.Atoi(strings.TrimPrefix(a.cfg.Port, ":")); err == nil && n > 0 {
		return n
	}
	return 8080
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ---- sources / status / config ----

func (a *App) handleSources(w http.ResponseWriter, r *http.Request) {
	cams, err := sources.ListCameras()
	if err != nil || len(cams) == 0 {
		// Graceful fallback so the UI never goes completely dead.
		cams = []sources.Camera{{ID: "video=Mevo Start", Name: "Mevo Start (Webcam Mode) [fallback]"}}
	}
	writeJSON(w, cams)
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	version := a.cfg.Version
	if version == "" {
		version = "dev"
	}
	writeJSON(w, map[string]string{
		"mode":    "spike",
		"version": version,
		"os":      runtime.GOOS,
	})
}

func (a *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, status.ServerConfig{
		Host:     a.host,
		Port:     a.httpPort(),
		RTMPPort: a.addrs.RTMP,
		HLSPort:  a.addrs.HLS,
	})
}

func (a *App) handleHLSJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(a.cfg.HLSJS)
}

func (a *App) handleQR(w http.ResponseWriter, r *http.Request) {
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
	_, _ = w.Write(png)
}

// ---- local stream lifecycle ----

type startReq struct {
	CameraID string `json:"cameraId"`
}

func (a *App) handleStreamStart(w http.ResponseWriter, r *http.Request) {
	var req startReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.CameraID == "" {
		http.Error(w, "cameraId required", http.StatusBadRequest)
		return
	}
	a.cams.StartLocal(req.CameraID, req.CameraID)
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleStreamStop(w http.ResponseWriter, r *http.Request) {
	var req startReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.CameraID == "" {
		http.Error(w, "cameraId required", http.StatusBadRequest)
		return
	}
	a.cams.Stop(req.CameraID)
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleActiveStreams(w http.ResponseWriter, r *http.Request) {
	devs, _ := a.cams.Snapshot()
	writeJSON(w, devs)
}

// ---- GameChanger ----

type gcStartReq struct {
	CameraPath string `json:"cameraPath"`
	GcServer   string `json:"gcServer"`
	GcKey      string `json:"gcKey"`
	GcFullUrl  string `json:"gcFullUrl"`
	Quality    string `json:"quality"`
}

func (a *App) handleGCStart(w http.ResponseWriter, r *http.Request) {
	var req gcStartReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[gamechanger] bad JSON in start request: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	quality := encode.NormalizeQuality(req.Quality)
	a.cams.SetQuality(quality)

	log.Printf("[gamechanger] start: cameraPath=%s hasFullUrl=%v gcServer=%s gcKeyLen=%d quality=%s",
		req.CameraPath, req.GcFullUrl != "", req.GcServer, len(req.GcKey), quality)

	if req.CameraPath == "" {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "cameraPath is required"})
		return
	}

	hasFull := strings.TrimSpace(req.GcFullUrl) != ""
	hasSeparate := strings.TrimSpace(req.GcServer) != "" && strings.TrimSpace(req.GcKey) != ""
	if !hasFull && !hasSeparate {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "Either gcFullUrl or both gcServer + gcKey must be provided"})
		return
	}

	rawID := a.cams.Resolve(req.CameraPath)

	// Destination: full URL wins; otherwise server + key.
	var dest string
	if req.GcFullUrl != "" {
		dest = req.GcFullUrl
	} else {
		dest = strings.TrimRight(req.GcServer, "/") + "/" + strings.TrimLeft(req.GcKey, "/")
	}

	cfg := status.GCConfig{FullURL: req.GcFullUrl, Server: req.GcServer, Key: req.GcKey, RawID: rawID}

	if err := a.cams.StartGC(rawID, req.CameraPath, dest, cfg); err != nil {
		log.Printf("[gamechanger] start rejected: %v", err)
		w.WriteHeader(http.StatusConflict)
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	log.Printf("[gamechanger] start accepted for %s (raw=%s) -> %s (quality=%s)", req.CameraPath, rawID, dest, quality)
	writeJSON(w, map[string]any{"ok": true, "camera": req.CameraPath, "dest": dest})
}

func (a *App) handleGCStop(w http.ResponseWriter, r *http.Request) {
	a.cams.StopActiveGC()
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleGCStatus(w http.ResponseWriter, r *http.Request) {
	_, g := a.cams.Snapshot()
	writeJSON(w, map[string]any{"active": g.Active, "path": g.Path, "camera": g.RawID})
}

// handleGCQuality sets the global broadcast quality and pushes the new state to all clients.
func (a *App) handleGCQuality(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Quality string `json:"quality"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	quality := encode.NormalizeQuality(req.Quality)
	a.cams.SetQuality(quality)
	log.Printf("[gamechanger] global quality set to %s", quality)
	a.broadcast()
	writeJSON(w, map[string]string{"quality": quality})
}

// ---- SSE ----

func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	log.Printf("[sse] client connected from %s", r.RemoteAddr)
	ch := a.hub.Subscribe()
	defer a.hub.Unsubscribe(ch)

	// Initial snapshot so the client is immediately up-to-date.
	if b, err := json.Marshal(a.buildSnapshot()); err == nil {
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

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

// ---- Wi-Fi ----

// handleWiFiNetworks performs a Wi-Fi scan and returns the visible networks.
// Scanning is slow (~10 s on some hardware), so a generous timeout is used.
// The response always includes a "networks" array (never null) so the UI can rely on
// Array.isArray. Errors are surfaced in-band so the UI can display them.
func (a *App) handleWiFiNetworks(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	nets, err := a.wifi.Scan(ctx)
	if nets == nil {
		nets = []wifi.Network{}
	}
	errStr := ""
	if err != nil {
		log.Printf("[wifi] scan error: %v", err)
		errStr = err.Error()
	}
	writeJSON(w, map[string]any{"networks": nets, "error": errStr})
}

// handleWiFiConnect joins a Wi-Fi network. Body: {"ssid":"...","password":"..."}.
func (a *App) handleWiFiConnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SSID     string `json:"ssid"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.SSID == "" {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"ok": false, "error": "ssid is required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if err := a.wifi.Connect(ctx, req.SSID, req.Password); err != nil {
		log.Printf("[wifi] connect %q: %v", req.SSID, err)
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	go a.broadcast()
	writeJSON(w, map[string]any{"ok": true, "error": ""})
}

// handleWiFiDisconnect drops the current upstream Wi-Fi connection.
func (a *App) handleWiFiDisconnect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	if err := a.wifi.Disconnect(ctx); err != nil {
		log.Printf("[wifi] disconnect: %v", err)
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	go a.broadcast()
	writeJSON(w, map[string]any{"ok": true, "error": ""})
}

// handleWiFiStatus returns the current cached Wi-Fi status.
func (a *App) handleWiFiStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.wifi.Status())
}

// ---- viewer page ----

func (a *App) handleWatch(w http.ResponseWriter, r *http.Request) {
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

	hlsURL := fmt.Sprintf("http://%s:%d/%s/index.m3u8", a.host, a.addrs.HLS, streamPath)
	rtmpURL := fmt.Sprintf("rtmp://%s:%d/%s", a.host, a.addrs.RTMP, streamPath)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, watchPage(streamPath, a.host, a.addrs.WebRTC, hlsURL, rtmpURL))
}
