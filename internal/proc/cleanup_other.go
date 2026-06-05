//go:build !windows

// Package proc provides startup port/process cleanup that frees resources held
// by previous instances of the application.
package proc

// KillStale is a no-op on non-Windows platforms.
func KillStale() {}

// Cleanup is a no-op on non-Windows platforms. The ports parameter is accepted
// to match the Windows implementation's signature.
func Cleanup(ports []int) {}
