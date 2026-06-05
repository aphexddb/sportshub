//go:build darwin

package ffmpeg

import (
	"errors"
	"os/exec"
)

// ensureFFmpeg looks for ffmpeg on PATH and returns it. macOS static builds are
// distributed as .7z/.zip which the stdlib cannot decode, so auto-download is
// not attempted; the user is asked to install ffmpeg instead.
func ensureFFmpeg() (string, error) {
	if path, err := exec.LookPath("ffmpeg"); err == nil {
		return path, nil
	}
	return "", errors.New("ffmpeg not found on PATH; install via 'brew install ffmpeg'")
}
