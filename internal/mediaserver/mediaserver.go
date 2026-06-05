// Package mediaserver runs MediaMTX natively, in-process, instead of downloading and
// supervising a mediamtx.exe subprocess. It wraps the MediaMTX core (exposed by the
// github.com/xaionaro-go/mediamtx fork, which republishes upstream's internal/ as pkg/)
// behind a small interface so the rest of the app never imports MediaMTX directly and the
// dependency stays swappable. Because the core is pure Go, the whole app cross-compiles to
// windows/linux/darwin on amd64+arm64 with no native binary to ship.
package mediaserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/xaionaro-go/mediamtx/pkg/core"
)

// Addrs are the listener ports MediaMTX exposes. They match the values baked into the
// generated config and the URLs the rest of the app builds.
type Addrs struct {
	RTMP   int // 1935 — publish/play RTMP (e.g. OBS)
	SRT    int // 8890 — local SRT publish/read (our ingest + GC pull)
	HLS    int // 8888 — HLS viewer
	WebRTC int // 8889 — low-latency WHEP viewer
	API    int // 9997 — control API (127.0.0.1 only)
}

// DefaultAddrs returns the historical port assignments used throughout the app.
func DefaultAddrs() Addrs {
	return Addrs{RTMP: 1935, SRT: 8890, HLS: 8888, WebRTC: 8889, API: 9997}
}

// Server is the in-process MediaMTX media server.
type Server struct {
	addrs   Addrs
	dir     string // directory the generated mediamtx.yml is written to
	apiBase string // e.g. http://127.0.0.1:9997

	mu    sync.Mutex
	core  *core.Core
	ready bool
}

// New builds a Server that will write its config under dir and listen on addrs.
// If dir is empty a per-user cache directory is used.
func New(dir string, addrs Addrs) *Server {
	if dir == "" {
		if cache, err := os.UserCacheDir(); err == nil {
			dir = filepath.Join(cache, "sportshub", "mediamtx")
		} else {
			dir = filepath.Join(os.TempDir(), "sportshub", "mediamtx")
		}
	}
	return &Server{
		addrs:   addrs,
		dir:     dir,
		apiBase: fmt.Sprintf("http://127.0.0.1:%d", addrs.API),
	}
}

// IsReady reports whether the server has started and its listeners are up.
func (s *Server) IsReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

// Addrs returns the configured listener ports.
func (s *Server) Addrs() Addrs { return s.addrs }

// Start writes the config, boots the in-process MediaMTX core, and waits for the key
// listeners to come up. It is safe to call once; subsequent calls are no-ops while running.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.core != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create media config dir: %w", err)
	}
	cfgPath := filepath.Join(s.dir, "mediamtx.yml")
	if err := os.WriteFile(cfgPath, []byte(s.configYAML()), 0o644); err != nil {
		return fmt.Errorf("write mediamtx.yml: %w", err)
	}

	log.Printf("[mediaserver] starting in-process MediaMTX (config %s)", cfgPath)
	c, ok := core.New([]string{cfgPath})
	if !ok {
		return fmt.Errorf("MediaMTX core failed to initialize (see logs above)")
	}

	s.mu.Lock()
	s.core = c
	s.mu.Unlock()

	// Surface an unexpected core exit (it should run until Close()).
	go func() {
		c.Wait()
		s.mu.Lock()
		s.ready = false
		s.mu.Unlock()
		log.Printf("[mediaserver] MediaMTX core stopped")
	}()

	// Wait for RTMP (TCP) first — the reliable signal the listeners initialized — then HLS/WebRTC.
	if err := waitForPort("tcp", fmt.Sprintf("127.0.0.1:%d", s.addrs.RTMP), 45*time.Second); err != nil {
		log.Printf("[mediaserver] WARNING: RTMP port %d not ready: %v", s.addrs.RTMP, err)
	}
	_ = waitForPort("tcp", fmt.Sprintf("127.0.0.1:%d", s.addrs.HLS), 10*time.Second)
	_ = waitForPort("tcp", fmt.Sprintf("127.0.0.1:%d", s.addrs.WebRTC), 10*time.Second)

	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
	log.Printf("[mediaserver] MediaMTX ready (RTMP %d, SRT %d, HLS %d, WebRTC %d, API %d)",
		s.addrs.RTMP, s.addrs.SRT, s.addrs.HLS, s.addrs.WebRTC, s.addrs.API)
	return nil
}

// Close stops the in-process core.
func (s *Server) Close() error {
	s.mu.Lock()
	c := s.core
	s.core = nil
	s.ready = false
	s.mu.Unlock()
	if c != nil {
		c.Close()
	}
	return nil
}

// PathReady reports whether MediaMTX has a live publisher with parsed tracks on the given
// path, plus a short human-readable reason for logging. This is the authoritative signal
// that an SRT reader can connect without being rejected with "no one is publishing".
//
// We query /v3/paths/list (not /v3/paths/get/<path>): the "get" endpoint logs a noisy
// "ERR [API] path not found" on every miss while we poll during startup; "list" does not.
func (s *Server) PathReady(path string) (bool, string) {
	client := http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(s.apiBase + "/v3/paths/list")
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

// WaitPathReady polls PathReady until the path is ready or timeout elapses, logging the
// first attempt, any change in reason, and the final outcome so the handshake is visible.
func (s *Server) WaitPathReady(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	start := time.Now()
	attempts := 0
	lastReason := ""
	for time.Now().Before(deadline) {
		attempts++
		ready, reason := s.PathReady(path)
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

// configYAML renders the MediaMTX config. Mirrors the historical spike config: open "all"
// path (no credentials), only the protocols we use, low-latency HLS for the browser fallback.
func (s *Server) configYAML() string {
	return fmt.Sprintf(`logLevel: info

rtmp: yes
rtmpAddress: :%d

srt: yes
srtAddress: :%d

api: yes
apiAddress: 127.0.0.1:%d
metrics: no
webrtc: yes
webrtcAddress: :%d
webrtcAllowOrigin: '*'
rtsp: no

hls: yes
hlsAddress: :%d
hlsAllowOrigin: '*'
# Low-Latency HLS so the browser fallback (and iOS Safari, which can't use WebRTC here)
# stays within ~1-2s instead of the 10-15s the default mpegts variant buffers.
hlsVariant: lowLatency
hlsSegmentCount: 7
hlsSegmentDuration: 1s
hlsPartDuration: 200ms

paths:
  all:
`, s.addrs.RTMP, s.addrs.SRT, s.addrs.API, s.addrs.WebRTC, s.addrs.HLS)
}

// waitForPort tries to connect to addr until timeout. Logs progress every ~5s.
func waitForPort(network, addr string, timeout time.Duration) error {
	if network == "" {
		network = "tcp"
	}
	deadline := time.Now().Add(timeout)
	start := time.Now()
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout(network, addr, 300*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		if time.Since(start) > 5*time.Second {
			log.Printf("[mediaserver] still waiting for port %s (%s) after %v...", addr, network, time.Since(start).Round(time.Second))
		}
		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s (%s) after %v", addr, network, timeout)
}
