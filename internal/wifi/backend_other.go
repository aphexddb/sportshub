//go:build !linux

package wifi

import (
	"context"
	"errors"

	"sportshub/internal/status"
)

// errNotSupported is returned by action methods on non-Linux platforms.
var errNotSupported = errors.New("wifi AP mode is only supported on Raspberry Pi (Linux)")

// stubBackend is the no-op backend used on non-Linux platforms.
type stubBackend struct{}

func newBackend() backend { return stubBackend{} }

func (stubBackend) supported() bool { return false }
func (stubBackend) apMAC() string   { return "" }

func (stubBackend) startAP(_ context.Context, _ string) error { return errNotSupported }
func (stubBackend) stopAP() error                             { return errNotSupported }
func (stubBackend) connect(_ context.Context, _, _ string) error {
	return errNotSupported
}
func (stubBackend) disconnect(_ context.Context) error { return errNotSupported }

func (stubBackend) scan(_ context.Context) ([]Network, error) {
	return nil, errNotSupported
}

func (stubBackend) refresh(_ context.Context) status.WiFiStatus {
	return status.WiFiStatus{Supported: false}
}
