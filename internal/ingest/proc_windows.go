//go:build windows

package ingest

import "os/exec"

// prepareCmd is a no-op on Windows (captures are single ffmpeg processes).
func prepareCmd(c *exec.Cmd) {}

// killCmd kills the capture process.
func killCmd(c *exec.Cmd) {
	if c.Process != nil {
		_ = c.Process.Kill()
	}
}
