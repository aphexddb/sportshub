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
	cfg := `logLevel: info

rtmp: yes
rtmpAddress: :1935

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

	// Quick check: is something already listening?
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:1935", 200*time.Millisecond); err == nil {
		conn.Close()
		log.Printf("[mediamtx] WARNING: Something is already listening on port 1935 before we started MediaMTX!")
	}

	if err := s.cmd.Start(); err != nil {
		return err
	}
	s.running = true

	// Actively wait for MediaMTX to accept connections on the RTMP port.
	// This is much more reliable than a fixed sleep.
	const readyTimeout = 20 * time.Second
	if err := waitForPort("127.0.0.1:1935", readyTimeout); err != nil {
		return fmt.Errorf("MediaMTX did not become ready on RTMP port 1935 within %s: %w", readyTimeout, err)
	}

	log.Printf("[mediamtx] MediaMTX RTMP port is ready")
	return nil
}

// waitForPort tries to TCP connect to addr until timeout.
func waitForPort(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", addr)
}

func osWriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}

