//go:build darwin

package ingest

import "os/exec"

// buildCaptureCmd builds the avfoundation ffmpeg capture for a macOS device. rawID is the
// device index reported by internal/sources (e.g. "0"). Video-only here; avfoundation audio
// is a separate device and is left out of the spike capture.
func buildCaptureCmd(ffmpegPath, rawID string, srtPort int, streamPath string) (*exec.Cmd, error) {
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
