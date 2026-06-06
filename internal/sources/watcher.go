package sources

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Watcher detects camera plug/unplug and invokes onChange whenever the available device list
// changes. On Linux it is event-driven (inotify on /dev for USB hotplug); on other platforms
// it polls. It diffs the enumerated list so onChange fires only on real changes, never on
// every tick. Callers should run it in a goroutine and cancel the context to stop it.
type Watcher struct {
	list     func() []Camera // enumerate the current devices (e.g. ListCameras)
	onChange func()          // invoked when the device list changes
	last     string          // fingerprint of the last seen list
}

// NewWatcher builds a watcher over the given enumeration function.
func NewWatcher(list func() []Camera, onChange func()) *Watcher {
	return &Watcher{list: list, onChange: onChange}
}

// Run watches until ctx is cancelled. It establishes a baseline immediately, then re-checks on
// each device event (Linux) or poll tick, firing onChange when the device set changes.
func (w *Watcher) Run(ctx context.Context) {
	events, poll := w.startEvents(ctx) // per-OS (watch_linux.go / watch_other.go)
	w.rescan()                         // baseline + emit the initial set

	var tick <-chan time.Time
	if poll > 0 {
		t := time.NewTicker(poll)
		defer t.Stop()
		tick = t.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			w.rescan()
		case _, ok := <-events:
			if !ok {
				events = nil // event source closed; fall through to poll (if any)
				continue
			}
			w.debounce(ctx, events) // coalesce the burst + let device nodes settle
			w.rescan()
		}
	}
}

// rescan enumerates and fires onChange iff the device set changed since last time.
func (w *Watcher) rescan() {
	if w.list == nil {
		return
	}
	fp := fingerprint(w.list())
	if fp != w.last {
		w.last = fp
		if w.onChange != nil {
			w.onChange()
		}
	}
}

// debounce waits out a burst of device events (a single USB plug emits several) so we
// re-enumerate once, after the new device nodes have settled.
func (w *Watcher) debounce(ctx context.Context, events <-chan struct{}) {
	timer := time.NewTimer(400 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				return
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(400 * time.Millisecond)
		case <-timer.C:
			return
		}
	}
}

// fingerprint is an order-independent signature of the device set, so reordering of device
// nodes doesn't look like a change.
func fingerprint(cams []Camera) string {
	ids := make([]string, len(cams))
	for i, c := range cams {
		ids[i] = c.ID + "|" + c.Name
	}
	sort.Strings(ids)
	return strings.Join(ids, "\n")
}
