package media

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
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
			return fmt.Errorf("MediaMTX failed to start: %w", err)
		}
	}

	ffmpegPath, _ := EnsureFFmpeg()

	// Normalize the dshow input.
	// The server list sends IDs like "video=Mevo-2GB5D", but sometimes we get just the name.
	input := cameraID
	if !strings.HasPrefix(strings.ToLower(input), "video=") {
		input = "video=" + input
	}

	videoName := input[6:] // strip "video="
	// Common pattern for USB webcams and Mevo Start in webcam mode:
	// video="Mevo-2GB5D"  ->  audio="Microphone (Mevo-2GB5D)"
	audioName := "Microphone (" + videoName + ")"
	inputSpec := fmt.Sprintf(`video=%s:audio=%s`, videoName, audioName)

	// Build a solid low-latency command for dshow webcams / Mevo Start.
	// We deliberately start at 720p30 — this is much more reliable on USB webcams than 1080p.
	// Include audio using the common "Microphone (Name)" pattern from the device list.
	// If audio device name differs, the stderr will show the error and we can adjust.
	args := []string{
		"-f", "dshow",
		"-rtbufsize", "200M",
		"-video_size", "1280x720",
		"-framerate", "30",
		"-use_wallclock_as_timestamps", "1",
		"-i", inputSpec,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-b:v", "3500k",
		"-maxrate", "4500k",
		"-bufsize", "7000k",
		"-g", "30",
		"-keyint_min", "30",
		"-pix_fmt", "yuv420p",
		"-fflags", "+genpts",
		"-c:a", "aac",
		"-b:a", "128k",
		"-ar", "48000",
		"-f", "flv",
		"rtmp://127.0.0.1:1935/" + streamPath,
	}

	log.Printf("[ingest] Starting ffmpeg for device: %s → path: %s", cameraID, streamPath)
	log.Printf("[ingest] ffmpeg %v", args)

	cmd := exec.Command(ffmpegPath, args...)

	// Capture stderr for diagnostics (critical when ingest fails)
	stderrPipe, _ := cmd.StderrPipe()
	go func() {
		if stderrPipe != nil {
			buf := make([]byte, 4096)
			for {
				n, err := stderrPipe.Read(buf)
				if n > 0 {
					log.Printf("[ffmpeg stderr] %s", string(buf[:n]))
				}
				if err != nil {
					break
				}
			}
		}
	}()

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
