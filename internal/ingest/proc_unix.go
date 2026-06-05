//go:build !windows

package ingest

import (
	"os/exec"
	"syscall"
)

// prepareCmd puts the capture command in its own process group so a shell pipeline's
// children (e.g. rpicam-vid + ffmpeg) can be signalled together.
func prepareCmd(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
}

// killCmd kills the whole process group (negative pid), so no rpicam-vid/ffmpeg child is
// left holding the camera. Falls back to killing just the leader if the group kill fails.
func killCmd(c *exec.Cmd) {
	if c.Process == nil {
		return
	}
	if err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL); err != nil {
		_ = c.Process.Kill()
	}
}
