//go:build windows

package ingest

import (
	"os/exec"
	"strings"
)

// buildCaptureCmd builds the DirectShow ffmpeg capture for a Windows device.
// The server list sends IDs like "video=Mevo-2GB5D"; sometimes we get just the name.
// Common pattern for USB webcams and Mevo Start in webcam mode:
//
//	video="Mevo-2GB5D"  ->  audio="Microphone (Mevo-2GB5D)"
func buildCaptureCmd(ffmpegPath, rawID string, srtPort int, streamPath string) (*exec.Cmd, error) {
	input := rawID
	if !strings.HasPrefix(strings.ToLower(input), "video=") {
		input = "video=" + input
	}
	videoName := input[6:] // strip "video="
	audioName := "Microphone (" + videoName + ")"
	spec := InputSpec{
		Format:   "dshow",
		PreInput: []string{"-rtbufsize", "200M", "-video_size", "1920x1080", "-framerate", "30"},
		Input:    "video=" + videoName + ":audio=" + audioName,
	}
	return exec.Command(ffmpegPath, buildIngestArgs(spec, srtPort, streamPath)...), nil
}
