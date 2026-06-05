//go:build !windows && !linux && !darwin

package ingest

import (
	"fmt"
	"os/exec"

	"sportshub2/internal/devices"
	"sportshub2/internal/ffmpeg"
)

// buildCaptureCmd is a compile-only fallback for unsupported OSes: a synthetic test source so
// the binary builds and runs even where real capture isn't wired up.
func buildCaptureCmd(rawID string, srtPort int, streamPath string) (*exec.Cmd, error) {
	ffmpegPath, err := ffmpeg.Path()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg unavailable: %w", err)
	}
	p := devices.ProfileFor(devices.KindStub)
	spec := InputSpec{
		Format:   "lavfi",
		PreInput: nil,
		Input:    fmt.Sprintf("testsrc=size=%dx%d:rate=%d", p.Width, p.Height, p.FPS),
	}
	return exec.Command(ffmpegPath, buildIngestArgs(spec, srtPort, streamPath)...), nil
}
