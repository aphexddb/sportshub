package app

import (
	"net/http"

	"sportshub/web"
)

// routes builds the HTTP handler. The paths and methods exactly match the contract the web UI
// in web/dist/index.html depends on. The handler is wrapped with captivePortal middleware.
func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	// Dashboard. Assets are embedded in the binary (or served from disk with -tags dev).
	// Disable caching so UI iterations show up immediately on phones/browsers.
	dashboard := http.FileServer(http.FS(web.Dist()))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		dashboard.ServeHTTP(w, r)
	})

	// Core API
	mux.HandleFunc("/api/sources", a.handleSources)
	mux.HandleFunc("/api/stream/start", a.handleStreamStart)
	mux.HandleFunc("/api/stream/stop", a.handleStreamStop)
	mux.HandleFunc("/api/status", a.handleStatus)
	mux.HandleFunc("/api/qr", a.handleQR)

	// GameChanger direct push
	mux.HandleFunc("/api/gamechanger/start", a.handleGCStart)
	mux.HandleFunc("/api/gamechanger/stop", a.handleGCStop)
	mux.HandleFunc("/api/gamechanger/status", a.handleGCStatus)
	mux.HandleFunc("/api/gamechanger/quality", a.handleGCQuality)
	mux.HandleFunc("/api/active-streams", a.handleActiveStreams)

	// SSE realtime status
	mux.HandleFunc("/api/events", a.handleEvents)

	// Public config + viewer page + embedded hls.js
	mux.HandleFunc("/api/config", a.handleConfig)
	mux.HandleFunc("/watch/", a.handleWatch)
	mux.HandleFunc("/static/hls.min.js", a.handleHLSJS)

	// Wi-Fi AP management
	mux.HandleFunc("/api/wifi/networks", a.handleWiFiNetworks)
	mux.HandleFunc("/api/wifi/connect", a.handleWiFiConnect)
	mux.HandleFunc("/api/wifi/disconnect", a.handleWiFiDisconnect)
	mux.HandleFunc("/api/wifi/status", a.handleWiFiStatus)

	return a.captivePortal(mux)
}
