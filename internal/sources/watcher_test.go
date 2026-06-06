package sources

import "testing"

func TestFingerprint_OrderIndependent(t *testing.T) {
	a := []Camera{{ID: "/dev/video0", Name: "Cam A"}, {ID: "libcamera:0", Name: "imx708"}}
	b := []Camera{{ID: "libcamera:0", Name: "imx708"}, {ID: "/dev/video0", Name: "Cam A"}}
	if fingerprint(a) != fingerprint(b) {
		t.Fatal("fingerprint should be order-independent")
	}
	c := append([]Camera{{ID: "/dev/video2", Name: "USB Webcam"}}, a...)
	if fingerprint(a) == fingerprint(c) {
		t.Fatal("adding a device must change the fingerprint")
	}
}

func TestWatcherRescan_FiresOnlyOnChange(t *testing.T) {
	// list returns a sequence of device snapshots; rescan should fire onChange only when the
	// set actually changes.
	snapshots := [][]Camera{
		{{ID: "libcamera:0", Name: "imx708"}},                                    // baseline
		{{ID: "libcamera:0", Name: "imx708"}},                                    // unchanged
		{{ID: "libcamera:0", Name: "imx708"}, {ID: "/dev/video0", Name: "C920"}}, // USB plugged
		{{ID: "libcamera:0", Name: "imx708"}, {ID: "/dev/video0", Name: "C920"}}, // unchanged
		{{ID: "libcamera:0", Name: "imx708"}},                                    // USB unplugged
	}
	i := 0
	calls := 0
	w := NewWatcher(
		func() []Camera {
			s := snapshots[i]
			if i < len(snapshots)-1 {
				i++
			}
			return s
		},
		func() { calls++ },
	)

	for range snapshots {
		w.rescan()
	}

	// baseline (1) + plug (1) + unplug (1) = 3 changes; the two "unchanged" rescans fire nothing.
	if calls != 3 {
		t.Fatalf("expected 3 onChange calls (baseline, plug, unplug), got %d", calls)
	}
}
