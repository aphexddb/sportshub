//go:build windows

// Package proc provides startup port/process cleanup that frees resources held
// by previous instances of the application.
package proc

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"
)

// KillStale quickly terminates any previous sportshub instances (except the current
// process) so a leftover instance can't hold the app's HTTP port. It does no port work
// and does not sleep, so it is cheap enough to run before binding the HTTP listener.
func KillStale() {
	ourPID := os.Getpid()
	psKill := fmt.Sprintf(`Get-Process -Name sportshub -ErrorAction SilentlyContinue | Where-Object { $_.Id -ne %d } | Stop-Process -Force -ErrorAction SilentlyContinue`, ourPID)
	exec.Command("powershell", "-NoProfile", "-Command", psKill).Run()
}

// Cleanup aggressively terminates any previous sportshub instances (except the
// current process) and frees the owners of the given TCP/UDP ports. It is a
// best-effort operation; individual failures are ignored.
func Cleanup(ports []int) {
	log.Printf("[startup] Cleaning up previous sportshub instances and freeing ports...")
	ourPID := os.Getpid()

	psKill := fmt.Sprintf(`Get-Process -Name sportshub -ErrorAction SilentlyContinue | Where-Object { $_.Id -ne %d } | Stop-Process -Force -ErrorAction SilentlyContinue`, ourPID)
	exec.Command("powershell", "-NoProfile", "-Command", psKill).Run()

	for _, p := range ports {
		psTCP := fmt.Sprintf(`Get-NetTCPConnection -LocalPort %d -ErrorAction SilentlyContinue | Select-Object -ExpandProperty OwningProcess -Unique | Where-Object { $_ -ne %d } | Stop-Process -Force -ErrorAction SilentlyContinue`, p, ourPID)
		exec.Command("powershell", "-NoProfile", "-Command", psTCP).Run()

		psUDP := fmt.Sprintf(`Get-NetUDPEndpoint -LocalPort %d -ErrorAction SilentlyContinue | Select-Object -ExpandProperty OwningProcess -Unique | Where-Object { $_ -ne %d } | Stop-Process -Force -ErrorAction SilentlyContinue`, p, ourPID)
		exec.Command("powershell", "-NoProfile", "-Command", psUDP).Run()
	}

	time.Sleep(700 * time.Millisecond)
	log.Printf("[startup] Port/process cleanup complete (our pid=%d).", ourPID)
}
