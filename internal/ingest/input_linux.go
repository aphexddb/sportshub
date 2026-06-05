//go:build linux

package ingest

import (
	"fmt"
	"os/exec"
	"strings"

	"sportshub2/internal/ffmpeg"
)

// buildCaptureCmd builds the capture command for a Linux camera.
//
//   - "libcamera:N" (Raspberry Pi CSI sensor, e.g. an Arducam imx708): the sensor is not a
//     plain V4L2 node, so we use rpicam-vid. It links libav, so it encodes H.264 and publishes
//     MPEG-TS straight to the media server over SRT — a single process, no ffmpeg or shell pipe.
//   - "/dev/videoN" (USB/UVC webcam): a normal ffmpeg V4L2 capture.
func buildCaptureCmd(rawID string, srtPort int, streamPath string) (*exec.Cmd, error) {
	if idx, ok := strings.CutPrefix(rawID, "libcamera:"); ok {
		if idx == "" {
			idx = "0"
		}
		bin := firstPath("rpicam-vid", "libcamera-vid")
		if bin == "" {
			return nil, fmt.Errorf("rpicam-vid not found (install rpicam-apps)")
		}
		// -o is a single argv element, so the SRT URL's '&'/'?' need no shell quoting.
		args := []string{
			"-t", "0", "--camera", idx,
			"--width", "1920", "--height", "1080", "--framerate", "30",
			"--bitrate", "6000000", "--codec", "h264",
			"--libav-format", "mpegts", "--inline", "--flush", "--nopreview",
			"-o", srtPublishURL(srtPort, streamPath),
		}
		return exec.Command(bin, args...), nil
	}

	// USB/UVC device path (e.g. /dev/video0).
	ffmpegPath, err := ffmpeg.Path()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg unavailable: %w", err)
	}
	spec := InputSpec{
		Format:   "v4l2",
		PreInput: []string{"-framerate", "30", "-video_size", "1920x1080"},
		Input:    rawID,
	}
	return exec.Command(ffmpegPath, buildIngestArgs(spec, srtPort, streamPath)...), nil
}

func firstPath(names ...string) string {
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}
