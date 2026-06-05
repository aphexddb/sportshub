//go:build linux

package ingest

import (
	"fmt"
	"os/exec"
	"strings"
)

// buildCaptureCmd builds the capture command for a Linux camera.
//
//   - "libcamera:N" (Raspberry Pi CSI sensor, e.g. an Arducam imx708): the sensor is not a
//     plain V4L2 capture node, so we use rpicam-vid (hardware H.264) and pipe it through
//     ffmpeg, which repackages to MPEG-TS over SRT with no re-encode. The pipeline runs under
//     /bin/sh; the ingest puts it in its own process group so rpicam + ffmpeg die together.
//   - "/dev/videoN" (USB/UVC webcam): a normal ffmpeg V4L2 capture.
func buildCaptureCmd(ffmpegPath, rawID string, srtPort int, streamPath string) (*exec.Cmd, error) {
	if idx, ok := strings.CutPrefix(rawID, "libcamera:"); ok {
		if idx == "" {
			idx = "0"
		}
		srt := srtPublishURL(srtPort, streamPath)
		// The SRT URL is single-quoted so the shell doesn't treat its '&'/'?' as operators.
		pipeline := fmt.Sprintf(
			"rpicam-vid -t 0 --camera %s --width 1920 --height 1080 --framerate 30 --bitrate 6000000 "+
				"--codec h264 --profile high --level 4.2 --inline --flush --nopreview -o - | "+
				"exec %s -loglevel warning -fflags nobuffer -use_wallclock_as_timestamps 1 "+
				"-f h264 -i pipe:0 -c copy -f mpegts '%s'",
			idx, ffmpegPath, srt)
		return exec.Command("/bin/sh", "-c", pipeline), nil
	}

	// USB/UVC device path (e.g. /dev/video0).
	spec := InputSpec{
		Format:   "v4l2",
		PreInput: []string{"-framerate", "30", "-video_size", "1920x1080"},
		Input:    rawID,
	}
	return exec.Command(ffmpegPath, buildIngestArgs(spec, srtPort, streamPath)...), nil
}
