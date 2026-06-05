//go:build darwin

package ingest

import (
	"fmt"
	"os/exec"

	"sportshub2/internal/devices"
	"sportshub2/internal/ffmpeg"
)

// buildCaptureCmd builds the avfoundation ffmpeg capture for a macOS device. rawID is the
// device index reported by internal/sources (e.g. "0"). Video-only here; avfoundation audio
// is a separate device and is left out of the spike capture.
func buildCaptureCmd(rawID string, srtPort int, streamPath string) (*exec.Cmd, error) {
	ffmpegPath, err := ffmpeg.Path()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg unavailable: %w", err)
	}
	if rawID == "" {
		rawID = "0"
	}
	p := devices.ProfileFor(devices.KindAVFoundation)
	spec := InputSpec{
		Format:   "avfoundation",
		PreInput: []string{"-framerate", fmt.Sprintf("%d", p.FPS), "-video_size", fmt.Sprintf("%dx%d", p.Width, p.Height)},
		Input:    rawID,
	}
	return exec.Command(ffmpegPath, buildIngestArgs(spec, srtPort, streamPath)...), nil
}
