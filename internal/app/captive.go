package app

import (
	"net/http"
	"strings"
)

// captiveProbes is the set of well-known OS captive-portal detection paths.
var captiveProbes = map[string]bool{
	"/generate_204":              true,
	"/gen_204":                   true,
	"/hotspot-detect.html":       true,
	"/library/test/success.html": true,
	"/ncsi.txt":                  true,
	"/connecttest.txt":           true,
	"/canonical.html":            true,
}

// captiveProbeHosts is the set of well-known OS connectivity-check hostnames.
// Requests to these hosts arriving on the AP network are redirected to the portal.
var captiveProbeHosts = map[string]bool{
	"connectivitycheck.gstatic.com": true,
	"clients3.google.com":           true,
	"captive.apple.com":             true,
	"www.msftconnecttest.com":       true,
	"detectportal.firefox.com":      true,
}

// captivePortal is middleware that implements a captive-portal redirect when the
// Wi-Fi AP is active. It is a no-op when the AP is off or on non-Pi hosts, so it
// is always safe to wrap the mux with it.
//
// Behaviour when the AP is active:
//   - Requests from the portal IP itself (Host == APIP) always pass through so the
//     dashboard loads normally once the client is redirected.
//   - API, static, and watch paths always pass through so handlers are never broken.
//   - Known OS captive-probe paths / hosts receive a 302 → http://<APIP>/.
//   - Any other request whose Host header does not contain the AP IP is also
//     redirected, catching browsers that request http://www.example.com/ before
//     the OS probe fires.
func (a *App) captivePortal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.wifi.APActive() {
			next.ServeHTTP(w, r)
			return
		}

		apIP := a.wifi.APIP()
		host := r.Host
		// Strip port from host if present.
		if idx := strings.LastIndex(host, ":"); idx != -1 {
			host = host[:idx]
		}

		// Always pass through requests that already target the AP IP, or the Pi's
		// upstream LAN IP, so the dashboard stays reachable from both networks.
		if host == apIP || (host != "" && host == a.wifi.Status().IP) {
			next.ServeHTTP(w, r)
			return
		}

		// Always pass through API / static / watch paths so handlers stay reachable.
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/static/") || strings.HasPrefix(p, "/watch/") {
			next.ServeHTTP(w, r)
			return
		}

		portalURL := "http://" + apIP + "/"

		// Redirect known OS captive-probe paths.
		if captiveProbes[p] {
			http.Redirect(w, r, portalURL, http.StatusFound)
			return
		}

		// Redirect known OS captive-probe hosts.
		if captiveProbeHosts[host] {
			http.Redirect(w, r, portalURL, http.StatusFound)
			return
		}

		// Redirect any cross-host request (client still thinks it's on the internet).
		if host != "" && !isLoopback(host) {
			http.Redirect(w, r, portalURL, http.StatusFound)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// isLoopback reports whether h (already stripped of port) is a loopback address or name.
func isLoopback(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}
