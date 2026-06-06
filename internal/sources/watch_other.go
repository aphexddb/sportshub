//go:build !linux

package sources

import (
	"context"
	"time"
)

// startEvents has no cheap OS hotplug event source off Linux, so the Watcher polls. The
// dshow (Windows) / avfoundation (macOS) enumeration runs ffmpeg, so keep the interval modest.
func (w *Watcher) startEvents(ctx context.Context) (<-chan struct{}, time.Duration) {
	return nil, 8 * time.Second
}
