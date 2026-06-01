package media

import (
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"
)

var (
	globalSupervisor *Supervisor
	ingests          = make(map[string]*exec.Cmd)
	ingestMu         sync.Mutex
)

// StartIngestForCamera starts ffmpeg capturing from a dshow device and pushing into MediaMTX.
// cameraID is the raw dshow name (e.g. "Mevo-2GB5D (046d:d119)").
func StartIngestForCamera(cameraID, streamPath string) error {
	ingestMu.Lock()
	defer ingestMu.Unlock()

	if _, exists := ingests[cameraID]; exists {
		return nil
	}

	if globalSupervisor == nil {
		globalSupervisor = NewSupervisor()
		if err := globalSupervisor.EnsureBinaries(); err != nil {
			return err
		}
		if err := globalSupervisor.StartMediaMTX(); err != nil {
			return fmt.Errorf("failed to start MediaMTX: %w", err)
		}
		// Give MediaMTX a bit more time to be ready
		time.Sleep(1200 * time.Millisecond)
	}

	ffmpegPath, _ := EnsureFFmpeg()

	// Build a solid low-latency command for dshow webcams / Mevo Start.
	// We prefer video-only first for reliability (audio device names often differ).
	// You can extend this later to detect and include audio.
	args := []string{
		"-f", "dshow",
		"-rtbufsize", "100M",
		"-i", "video=" + cameraID,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-b:v", "4000k",
		"-maxrate", "5000k",
		"-bufsize", "8000k",
		"-g", "30",
		"-keyint_min", "30",
		"-pix_fmt", "yuv420p",
		"-f", "flv",
		"rtmp://127.0.0.1:1935/" + streamPath,
	}

	log.Printf("[ingest] Starting ffmpeg for device: %s → path: %s", cameraID, streamPath)
	log.Printf("[ingest] ffmpeg %v", args)

	cmd := exec.Command(ffmpegPath, args...)
	cmd.Stdout = nil
	// Capture stderr so we can see useful errors in the console
	cmd.Stderr = nil // we can improve this later with pipes if needed

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	ingests[cameraID] = cmd

	// Monitor the process in background so we notice if it dies quickly
	go func() {
		err := cmd.Wait()
		ingestMu.Lock()
		delete(ingests, cameraID)
		ingestMu.Unlock()
		if err != nil {
			log.Printf("[ingest] ffmpeg for %s exited: %v", cameraID, err)
		} else {
			log.Printf("[ingest] ffmpeg for %s exited cleanly", cameraID)
		}
	}()

	return nil
}

func StopIngest(cameraID string) {
	ingestMu.Lock()
	defer ingestMu.Unlock()

	if cmd, ok := ingests[cameraID]; ok {
		log.Printf("[ingest] Stopping ingest for %s", cameraID)
		_ = cmd.Process.Kill()
		delete(ingests, cameraID)
	}
}

// GetActiveIngests returns currently running ingests (for UI status if we want it later)
func GetActiveIngests() map[string]bool {
	ingestMu.Lock()
	defer ingestMu.Unlock()

	out := make(map[string]bool, len(ingests))
	for k := range ingests {
		out[k] = true
	}
	return out
}
