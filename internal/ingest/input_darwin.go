//go:build darwin

package ingest

import (
	"fmt"
	"os/exec"

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
	spec := InputSpec{
		Format:   "avfoundation",
		PreInput: []string{"-framerate", "30", "-video_size", "1920x1080"},
		Input:    rawID,
	}
	return exec.Command(ffmpegPath, buildIngestArgs(spec, srtPort, streamPath)...), nil
}
