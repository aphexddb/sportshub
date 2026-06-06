//go:build linux

package sources

import (
	"context"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// startEvents watches /dev for video/media device-node create+remove (USB camera hotplug) via
// inotify, returning a coalesced signal channel. poll is 0 because the events cover hotplug, so
// we never periodically run rpicam-hello (which would briefly touch the CSI camera). If the
// inotify watcher can't be created, it falls back to a slow safety poll.
func (w *Watcher) startEvents(ctx context.Context) (<-chan struct{}, time.Duration) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("[sources] device watch unavailable (%v); falling back to poll", err)
		return nil, 30 * time.Second
	}
	if err := fw.Add("/dev"); err != nil {
		log.Printf("[sources] cannot watch /dev (%v); falling back to poll", err)
		_ = fw.Close()
		return nil, 30 * time.Second
	}

	out := make(chan struct{}, 1)
	go func() {
		defer fw.Close()
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-fw.Events:
				if !ok {
					return
				}
				base := filepath.Base(ev.Name)
				isVideo := strings.HasPrefix(base, "video") || strings.HasPrefix(base, "media")
				if isVideo && ev.Op&(fsnotify.Create|fsnotify.Remove) != 0 {
					select {
					case out <- struct{}{}: // signal; coalesced by the Watcher's debounce
					default:
					}
				}
			case _, ok := <-fw.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	log.Printf("[sources] watching /dev for camera hotplug (inotify)")
	return out, 0
}
