package media

import (
	"fmt"
	"os/exec"
	"sync"
)

var (
	globalSupervisor *Supervisor
	ingests          = make(map[string]*exec.Cmd)
	ingestMu         sync.Mutex
)

// StartIngestForCamera starts ffmpeg capturing from a dshow device and pushing to MediaMTX
func StartIngestForCamera(cameraID, rtmpURL string) error {
	ingestMu.Lock()
	defer ingestMu.Unlock()

	if _, exists := ingests[cameraID]; exists {
		return nil // already running
	}

	if globalSupervisor == nil {
		globalSupervisor = NewSupervisor()
		if err := globalSupervisor.EnsureBinaries(); err != nil {
			return err
		}
		if err := globalSupervisor.StartMediaMTX(); err != nil {
			return fmt.Errorf("failed to start MediaMTX: %w", err)
		}
	}

	ffmpegPath, _ := EnsureFFmpeg() // we know it exists after EnsureBinaries

	// dshow input for video + audio (Mevo Start usually has both)
	// -f dshow -i video="Exact Name":audio="Exact Name" is the correct syntax
	args := []string{
		"-f", "dshow",
		"-i", fmt.Sprintf("video=%s:audio=%s", cameraID, cameraID), // will often work; fallback below if needed
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-c:a", "aac",
		"-b:a", "128k",
		"-f", "flv",
		rtmpURL,
	}

	cmd := exec.Command(ffmpegPath, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil // in real version we will capture logs

	if err := cmd.Start(); err != nil {
		// Many cameras have separate audio device names. Try video-only as fallback.
		args = []string{
			"-f", "dshow",
			"-i", "video=" + cameraID,
			"-c:v", "libx264",
			"-preset", "veryfast",
			"-tune", "zerolatency",
			"-f", "flv",
			rtmpURL,
		}
		cmd = exec.Command(ffmpegPath, args...)
		if err := cmd.Start(); err != nil {
			return err
		}
	}

	ingests[cameraID] = cmd
	return nil
}

func StopIngest(cameraID string) {
	ingestMu.Lock()
	defer ingestMu.Unlock()

	if cmd, ok := ingests[cameraID]; ok {
		_ = cmd.Process.Kill()
		delete(ingests, cameraID)
	}
}
