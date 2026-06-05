//go:build !windows && !linux && !darwin

package ingest

import "os/exec"

// buildCaptureCmd is a compile-only fallback for unsupported OSes: a synthetic test source so
// the binary builds and runs even where real capture isn't wired up.
func buildCaptureCmd(ffmpegPath, rawID string, srtPort int, streamPath string) (*exec.Cmd, error) {
	spec := InputSpec{
		Format:   "lavfi",
		PreInput: nil,
		Input:    "testsrc=size=1920x1080:rate=30",
	}
	return exec.Command(ffmpegPath, buildIngestArgs(spec, srtPort, streamPath)...), nil
}
