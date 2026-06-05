//go:build linux

package ffmpeg

import (
	"errors"
	"os/exec"
)

// ensureFFmpeg looks for ffmpeg on PATH and returns it. Linux static builds are
// distributed as .tar.xz which the stdlib cannot decode, so auto-download is
// not attempted; the user is asked to install ffmpeg instead.
func ensureFFmpeg() (string, error) {
	if path, err := exec.LookPath("ffmpeg"); err == nil {
		return path, nil
	}
	return "", errors.New("ffmpeg not found on PATH; install via your package manager (e.g. apt install ffmpeg)")
}
