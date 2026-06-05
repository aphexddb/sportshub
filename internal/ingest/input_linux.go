//go:build linux

package ingest

import (
	"fmt"
	"os/exec"
	"strings"

	"sportshub2/internal/devices"
	"sportshub2/internal/ffmpeg"
)

// buildCaptureCmd builds the capture command for a Linux camera, using the device-kind capture
// profile (resolution/framerate/bitrate) from internal/devices.
//
//   - "libcamera:N" (Raspberry Pi CSI sensor, e.g. an Arducam imx708): the sensor is not a
//     plain V4L2 node. rpicam-vid captures raw frames; ffmpeg does the single H.264 encode so
//     it owns timestamps. (rpicam's muxed output ships broken PTS — MediaMTX then can't segment
//     it for HLS, and -c copy of raw H.264 leaves timestamps unset.) Baseline + CFR gives a
//     clean, browser-friendly stream. The pipeline runs under /bin/sh in its own process group
//     so rpicam + ffmpeg die together.
//   - "/dev/videoN" (USB/UVC webcam): a normal ffmpeg V4L2 capture.
func buildCaptureCmd(rawID string, srtPort int, streamPath string) (*exec.Cmd, error) {
	if idx, ok := strings.CutPrefix(rawID, "libcamera:"); ok {
		if idx == "" {
			idx = "0"
		}
		rpicam := firstPath("rpicam-vid", "libcamera-vid")
		if rpicam == "" {
			return nil, fmt.Errorf("rpicam-vid not found (install rpicam-apps)")
		}
		ffmpegPath, err := ffmpeg.Path()
		if err != nil {
			return nil, fmt.Errorf("ffmpeg unavailable: %w", err)
		}
		p := devices.ProfileFor(devices.KindCSI)
		size := fmt.Sprintf("%dx%d", p.Width, p.Height)
		// SRT URL single-quoted so the shell leaves its '&'/'?' alone.
		pipeline := fmt.Sprintf(
			"%s -t 0 --camera %s --width %d --height %d --framerate %d --codec yuv420 --flush --nopreview -o - | "+
				"exec %s -loglevel warning -f rawvideo -pix_fmt yuv420p -s:v %s -framerate %d -i pipe:0 "+
				"-c:v libx264 -preset ultrafast -tune zerolatency -profile:v baseline -pix_fmt yuv420p "+
				"-g %d -keyint_min %d -b:v %s -maxrate %s -bufsize 2000k -bf 0 -f mpegts '%s'",
			rpicam, idx, p.Width, p.Height, p.FPS,
			ffmpegPath, size, p.FPS,
			p.FPS, p.FPS, p.Bitrate, p.Bitrate, srtPublishURL(srtPort, streamPath))
		return exec.Command("/bin/sh", "-c", pipeline), nil
	}

	// USB/UVC device path (e.g. /dev/video0).
	ffmpegPath, err := ffmpeg.Path()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg unavailable: %w", err)
	}
	p := devices.ProfileFor(devices.KindUSB)
	spec := InputSpec{
		Format:   "v4l2",
		PreInput: []string{"-framerate", fmt.Sprintf("%d", p.FPS), "-video_size", fmt.Sprintf("%dx%d", p.Width, p.Height)},
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
