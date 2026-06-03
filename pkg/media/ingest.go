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
	initOnce         sync.Once
	initErr          error
)

type StreamStats struct {
	FPS     float64 `json:"fps"`
	Bitrate string  `json:"bitrate"`
	Speed   string  `json:"speed"`
	Frames  int     `json:"frames"`
}

var (
	statsMu     sync.Mutex
	streamStats = make(map[string]StreamStats) // rawID -> latest
	notifier    func(event string, payload any)
)

// SetNotifier lets the HTTP server register a callback so that ingest lifecycle
// and live stats can drive SSE broadcasts.
func SetNotifier(n func(string, any)) { notifier = n }

func notify(event string, payload any) {
	if notifier != nil {
		notifier(event, payload)
	}
}

// GetStreamStats returns a copy of the latest per-raw-device ffmpeg progress stats.
func GetStreamStats() map[string]StreamStats {
	statsMu.Lock()
	defer statsMu.Unlock()
	out := make(map[string]StreamStats, len(streamStats))
	for k, v := range streamStats {
		out[k] = v
	}
	return out
}

// ParseFFmpegProgressLine extracts common progress fields from a single ffmpeg stderr line.
// Used by both local ingest and GC restream stderr readers.
func ParseFFmpegProgressLine(line string) (fps float64, bitrate, speed string, frames int) {
	lower := strings.ToLower(line)
	if idx := strings.Index(lower, "frame="); idx >= 0 {
		rest := strings.TrimSpace(line[idx+6:])
		fmt.Sscanf(rest, "%d", &frames)
	}
	if idx := strings.Index(lower, "fps="); idx >= 0 {
		rest := strings.TrimSpace(line[idx+4:])
		fmt.Sscanf(rest, "%f", &fps)
	}
	if idx := strings.Index(lower, "bitrate="); idx >= 0 {
		rest := strings.TrimSpace(line[idx+8:])
		if i := strings.IndexAny(rest, " \t\r\n"); i >= 0 {
			bitrate = rest[:i]
		} else {
			bitrate = rest
		}
	}
	if idx := strings.Index(lower, "speed="); idx >= 0 {
		rest := strings.TrimSpace(line[idx+6:])
		if i := strings.IndexAny(rest, " \t\r\n"); i >= 0 {
			speed = rest[:i]
		} else {
			speed = rest
		}
	}
	return
}

func parseAndUpdateStats(rawID, chunk string) {
	for _, ln := range strings.Split(chunk, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		fps, br, spd, fr := ParseFFmpegProgressLine(ln)
		if fps != 0 || br != "" || fr != 0 {
			statsMu.Lock()
			cur := streamStats[rawID]
			if fps != 0 {
				cur.FPS = fps
			}
			if br != "" {
				cur.Bitrate = br
			}
			if spd != "" {
				cur.Speed = spd
			}
			if fr != 0 {
				cur.Frames = fr
			}
			streamStats[rawID] = cur
			statsMu.Unlock()
			notify("stats", map[string]any{"rawId": rawID})
		}
	}
}

func InitMedia() error {
	initOnce.Do(func() {
		globalSupervisor = NewSupervisor()
		if err := globalSupervisor.EnsureBinaries(); err != nil {
			initErr = err
			return
		}
		if err := globalSupervisor.StartMediaMTX(); err != nil {
			initErr = fmt.Errorf("MediaMTX failed to start: %w", err)
			return
		}
	})
	return initErr
}

// StartIngestForCamera starts ffmpeg capturing from a dshow device and pushing into MediaMTX.
// cameraID is the raw dshow name (e.g. "Mevo-2GB5D (046d:d119)").
func StartIngestForCamera(cameraID, streamPath string) error {
	ingestMu.Lock()
	defer ingestMu.Unlock()

	if _, exists := ingests[cameraID]; exists {
		return nil
	}

	if err := InitMedia(); err != nil {
		return err
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
	// Capture at 1080p30 as GameChanger wants high quality 1080p for the final push.
	// (USB webcams can be finicky at 1080p; -err_detect ignore_err helps with common EOI issues.)
	// Include audio using the common "Microphone (Name)" pattern from the device list.
	// If audio device name differs, the stderr will show the error and we can adjust.
	args := []string{
		"-f", "dshow",
		"-rtbufsize", "200M",
		"-video_size", "1920x1080",
		"-framerate", "30",
		"-use_wallclock_as_timestamps", "1",
		"-err_detect", "ignore_err",
		"-i", inputSpec,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-b:v", "5000k",
		"-maxrate", "6000k",
		"-bufsize", "8000k",
		"-g", "15",  // lower GOP for reduced latency
		"-keyint_min", "15",
		"-pix_fmt", "yuv420p",
		"-fflags", "+genpts",
		"-c:a", "aac",
		"-b:a", "128k",
		"-ar", "48000",
		"-f", "mpegts",
		fmt.Sprintf("srt://127.0.0.1:8890?streamid=publish:%s&latency=100000&mode=caller", streamPath),
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
					s := string(buf[:n])
					log.Printf("[ffmpeg stderr] %s", s)
					parseAndUpdateStats(cameraID, s)
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

	// reset stats for this device
	statsMu.Lock()
	streamStats[cameraID] = StreamStats{}
	statsMu.Unlock()

	ingests[cameraID] = cmd
	notify("ingest-started", map[string]any{"rawId": cameraID, "path": streamPath})

	// Monitor the process in background so we notice if it dies quickly
	go func() {
		err := cmd.Wait()
		ingestMu.Lock()
		delete(ingests, cameraID)
		ingestMu.Unlock()

		statsMu.Lock()
		delete(streamStats, cameraID)
		statsMu.Unlock()

		notify("ingest-stopped", map[string]any{"rawId": cameraID})
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

		statsMu.Lock()
		delete(streamStats, cameraID)
		statsMu.Unlock()

		notify("ingest-stopped", map[string]any{"rawId": cameraID})
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
