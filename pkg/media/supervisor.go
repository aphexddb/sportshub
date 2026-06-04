package media

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type Supervisor struct {
	mtxPath string
	cmd     *exec.Cmd
	mu      sync.Mutex
	running bool
}

var (
	mtxReady   bool
	mtxReadyMu sync.Mutex
)

// IsMediaReady reports whether MediaMTX has successfully started and is listening.
func IsMediaReady() bool {
	mtxReadyMu.Lock()
	defer mtxReadyMu.Unlock()
	return mtxReady
}

func NewSupervisor() *Supervisor {
	return &Supervisor{}
}

// EnsureBinaries downloads MediaMTX + ffmpeg if needed (Windows only for now)
func (s *Supervisor) EnsureBinaries() error {
	mtx, err := EnsureMediaMTX()
	if err != nil {
		return err
	}
	s.mtxPath = mtx

	_, err = EnsureFFmpeg()
	return err
}

// StartMediaMTX starts the MediaMTX server (very minimal config for the spike)
func (s *Supervisor) StartMediaMTX() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	binDir := filepath.Dir(s.mtxPath)
	configPath := filepath.Join(binDir, "mediamtx.yml")

	// Modern config for MediaMTX v1.x
	// We use an open "all" path so any client can publish (no credentials required).
	// Only bind addresses for protocols we enable. Setting webrtcAddress while webrtc:no
	// can cause spurious "listen udp :8000" errors.
	cfg := `logLevel: info

rtmp: yes
rtmpAddress: :1935

srt: yes
srtAddress: :8890

api: yes
apiAddress: 127.0.0.1:9997
metrics: no
webrtc: yes
webrtcAddress: :8889
webrtcAllowOrigin: '*'
rtsp: no

hls: yes
hlsAddress: :8888
hlsAllowOrigin: '*'
# Low-Latency HLS so the browser fallback (and iOS Safari, which can't use WebRTC here)
# stays within ~1-2s instead of the 10-15s the default mpegts variant buffers.
hlsVariant: lowLatency
hlsSegmentCount: 7
hlsSegmentDuration: 1s
hlsPartDuration: 200ms

paths:
  all:
`

	if err := osWriteFile(configPath, []byte(cfg), 0644); err != nil {
		return err
	}

	// MediaMTX v1.x takes the config path as a positional argument, not -config
	s.cmd = exec.Command(s.mtxPath, configPath)
	s.cmd.Dir = binDir

	// Forward MediaMTX logs to our console so we can see if it has errors
	s.cmd.Stdout = os.Stdout
	s.cmd.Stderr = os.Stderr

	log.Printf("[mediamtx] Starting MediaMTX from %s", s.mtxPath)

	// Kill any previous instance to avoid port conflicts (Windows)
	exec.Command("taskkill", "/F", "/T", "/IM", "mediamtx.exe").Run() // ignore error if not running

	// Quick check: is something already listening?
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:1935", 200*time.Millisecond); err == nil {
		conn.Close()
		log.Printf("[mediamtx] WARNING: Something is already listening on port 1935 before we started MediaMTX!")
	}

	if err := s.cmd.Start(); err != nil {
		return err
	}
	s.running = true

	// Wait for RTMP (TCP 1935) first — this is the reliable signal that MediaMTX has initialized its listeners.
	const readyTimeout = 45 * time.Second
	if err := waitForPort("tcp", "127.0.0.1:1935", readyTimeout); err != nil {
		log.Printf("[mediamtx] WARNING: RTMP port 1935 not ready after start: %v", err)
	} else {
		log.Printf("[mediamtx] RTMP port 1935 is ready")
	}

	// SRT is UDP. After RTMP is up the SRT listener is also up (same startup). Give a grace for full path setup.
	time.Sleep(500 * time.Millisecond)

	// UDP dial "connects" locally quickly if the port was acquired; not a full SRT handshake probe but sufficient post-TCP-wait.
	if conn, err := net.DialTimeout("udp", "127.0.0.1:8890", 250*time.Millisecond); err == nil {
		conn.Close()
		log.Printf("[mediamtx] SRT UDP port 8890 appears bound")
	}

	// Wait for HLS (now enabled for local viewer / QR code streaming)
	if err := waitForPort("tcp", "127.0.0.1:8888", 10*time.Second); err != nil {
		log.Printf("[mediamtx] WARNING: HLS port 8888 not ready after start: %v", err)
	} else {
		log.Printf("[mediamtx] HLS port 8888 is ready")
	}

	// Wait for WebRTC (for low-latency local viewing via /watch/)
	if err := waitForPort("tcp", "127.0.0.1:8889", 10*time.Second); err != nil {
		log.Printf("[mediamtx] WARNING: WebRTC port 8889 not ready after start: %v", err)
	} else {
		log.Printf("[mediamtx] WebRTC port 8889 is ready")
	}

	log.Printf("[mediamtx] MediaMTX is ready (RTMP + SRT + HLS + WebRTC)")

	mtxReadyMu.Lock()
	mtxReady = true
	mtxReadyMu.Unlock()
	return nil
}

// waitForPort tries to connect (tcp or udp) to addr until timeout. Logs progress every ~5s.
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
			log.Printf("[mediamtx] port %s (%s) ready after %v", addr, network, time.Since(start))
			return nil
		}
		if time.Since(start) > 5*time.Second {
			log.Printf("[mediamtx] still waiting for port %s (%s) after %v...", addr, network, time.Since(start))
		}
		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s (%s) after %v", addr, network, timeout)
}

func osWriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}
