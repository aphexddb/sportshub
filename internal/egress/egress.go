// Package egress runs the GameChanger restream ffmpeg: it pulls a published path from the
// media server over SRT, re-encodes to the requested broadcast quality, and pushes FLV to
// the destination (RTMP or RTMPS). It satisfies camera.Restreamer. Teardown is decided by
// the camera state machine (demand-based), not by scraping ffmpeg stderr.
package egress

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"

	"sportshub/internal/camera"
	"sportshub/internal/encode"
	"sportshub/internal/ffmpeg"
	"sportshub/internal/status"
)

// Egress launches GameChanger push processes.
type Egress struct {
	srtPort int
}

// New builds an Egress that pulls from the media server's SRT port.
func New(srtPort int) *Egress { return &Egress{srtPort: srtPort} }

// handle controls one running push process and satisfies camera.RestreamHandle.
type handle struct {
	cmd  *exec.Cmd
	once sync.Once
}

func (h *handle) Stop() {
	h.once.Do(func() {
		if h.cmd != nil && h.cmd.Process != nil {
			_ = h.cmd.Process.Kill()
		}
	})
}

// Start launches the push for req and returns a handle. Progress is delivered via
// req.OnStats (cumulative snapshots) and process exit via req.OnExit.
func (e *Egress) Start(req camera.RestreamRequest) (camera.RestreamHandle, error) {
	ffmpegPath, err := ffmpeg.Path()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg unavailable: %w", err)
	}

	enc := encode.ParamsFor(req.Quality)
	args := buildGCArgs(req.Path, req.Dest, enc, e.srtPort, req.PreviewPath)
	log.Printf("[gamechanger] %s pull cam=%s → %s (quality=%s, preview=%s)", req.RawID, req.Path, req.Dest, encode.NormalizeQuality(req.Quality), req.PreviewPath)
	log.Printf("[gamechanger] ffmpeg %v", args)

	cmd := exec.Command(ffmpegPath, args...)
	stderrPipe, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ffmpeg start failed: %w", err)
	}

	h := &handle{cmd: cmd}

	// Reader: accumulate egress stats and push snapshots to the state machine.
	go func() {
		if stderrPipe == nil {
			return
		}
		var cur status.StreamStats
		buf := make([]byte, 4096)
		for {
			n, rerr := stderrPipe.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				log.Printf("[gamechanger ffmpeg stderr] %s", chunk)
				if updateStats(&cur, chunk) && req.OnStats != nil {
					req.OnStats(cur)
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// Monitor: report exit to the state machine.
	go func() {
		werr := cmd.Wait()
		if req.OnExit != nil {
			req.OnExit(werr)
		}
	}()

	return h, nil
}

// updateStats merges ffmpeg progress fields (keeping last non-zero values) into cur.
// Returns true if anything changed.
func updateStats(cur *status.StreamStats, chunk string) bool {
	changed := false
	for _, ln := range strings.Split(chunk, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		fps, br, spd, fr := ffmpeg.ParseFFmpegProgressLine(ln)
		if fps == 0 && br == "" && fr == 0 {
			continue
		}
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
		changed = true
	}
	return changed
}
