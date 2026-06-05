// Package ingest runs the local capture ffmpeg for each camera: it captures from the OS
// device (dshow/avfoundation/v4l2) and publishes an H.264/AAC MPEG-TS stream into the media
// server over SRT. It satisfies camera.Capturer. Process management and ffmpeg-stderr stats
// parsing live here; the arg building (args.go) is pure and unit-tested.
package ingest

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"

	"sportshub2/internal/ffmpeg"
	"sportshub2/internal/status"
)

// Ingest manages all active capture processes.
type Ingest struct {
	srtPort int

	onStats func()             // invoked when progress stats change (e.g. broadcast)
	onExit  func(rawID string) // invoked when a capture process exits

	mu    sync.Mutex
	procs map[string]*exec.Cmd
	stats map[string]status.StreamStats
}

// New builds an Ingest publishing to the media server's SRT port. onStats fires on every
// progress update; onExit fires when any capture process exits (wire it to the camera
// manager's HandleIngestExit so unexpected camera loss surfaces as an error).
func New(srtPort int, onStats func(), onExit func(rawID string)) *Ingest {
	return &Ingest{
		srtPort: srtPort,
		onStats: onStats,
		onExit:  onExit,
		procs:   make(map[string]*exec.Cmd),
		stats:   make(map[string]status.StreamStats),
	}
}

// Start launches ffmpeg capturing from rawID and publishing to streamPath. It is a no-op
// if a capture for rawID is already running.
func (i *Ingest) Start(rawID, streamPath string) error {
	i.mu.Lock()
	if _, exists := i.procs[rawID]; exists {
		i.mu.Unlock()
		return nil
	}
	i.mu.Unlock()

	cmd, err := buildCaptureCmd(rawID, i.srtPort, streamPath)
	if err != nil {
		return err
	}
	prepareCmd(cmd) // own process group on unix so pipelines (rpicam | ffmpeg) die together
	log.Printf("[ingest] starting capture for device %s → path %s", rawID, streamPath)
	log.Printf("[ingest] %s %v", cmd.Path, cmd.Args)

	stderrPipe, _ := cmd.StderrPipe()
	go i.readStderr(rawID, stderrPipe)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start capture: %w", err)
	}

	i.mu.Lock()
	i.procs[rawID] = cmd
	i.stats[rawID] = status.StreamStats{}
	i.mu.Unlock()

	// Monitor exit so we notice if capture dies (camera unplugged, ffmpeg crash).
	go func() {
		err := cmd.Wait()
		i.mu.Lock()
		delete(i.procs, rawID)
		delete(i.stats, rawID)
		i.mu.Unlock()
		if err != nil {
			log.Printf("[ingest] ffmpeg for %s exited: %v", rawID, err)
		} else {
			log.Printf("[ingest] ffmpeg for %s exited cleanly", rawID)
		}
		if i.onExit != nil {
			i.onExit(rawID)
		}
	}()

	return nil
}

// Stop kills the capture process for rawID (if any). The exit monitor still fires onExit.
func (i *Ingest) Stop(rawID string) {
	i.mu.Lock()
	cmd := i.procs[rawID]
	delete(i.procs, rawID)
	delete(i.stats, rawID)
	i.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		log.Printf("[ingest] stopping capture for %s", rawID)
		killCmd(cmd) // kills the whole process group (rpicam + ffmpeg), not just the leader
	}
}

// Stats returns a copy of the latest per-device ffmpeg progress stats.
func (i *Ingest) Stats() map[string]status.StreamStats {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make(map[string]status.StreamStats, len(i.stats))
	for k, v := range i.stats {
		out[k] = v
	}
	return out
}

// Active returns the raw ids of currently-running captures.
func (i *Ingest) Active() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]string, 0, len(i.procs))
	for k := range i.procs {
		out = append(out, k)
	}
	return out
}

func (i *Ingest) readStderr(rawID string, pipe interface{ Read([]byte) (int, error) }) {
	if pipe == nil {
		return
	}
	buf := make([]byte, 4096)
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			s := string(buf[:n])
			log.Printf("[ffmpeg stderr] %s", s)
			i.parseAndUpdate(rawID, s)
		}
		if err != nil {
			return
		}
	}
}

// parseAndUpdate merges ffmpeg progress fields (keeping last non-zero values) and notifies.
func (i *Ingest) parseAndUpdate(rawID, chunk string) {
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
		i.mu.Lock()
		cur := i.stats[rawID]
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
		i.stats[rawID] = cur
		i.mu.Unlock()
		changed = true
	}
	if changed && i.onStats != nil {
		i.onStats()
	}
}
