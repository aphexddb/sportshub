package media

import (
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

	// Write a minimal config that enables RTMP on default port
	cfg := `logLevel: info
rtmp: yes
rtmpAddress: :1935
paths:
  all:
    publish: yes
`
	if err := osWriteFile(configPath, []byte(cfg), 0644); err != nil {
		return err
	}

	s.cmd = exec.Command(s.mtxPath, "-config", configPath)
	s.cmd.Dir = binDir

	if err := s.cmd.Start(); err != nil {
		return err
	}
	s.running = true

	// Give it a moment to bind the port
	time.Sleep(800 * time.Millisecond)
	return nil
}

func osWriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}

