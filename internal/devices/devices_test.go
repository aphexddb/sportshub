package devices

import (
	"strings"
	"testing"
)

func TestPiCameraModel(t *testing.T) {
	cases := map[string]string{
		"imx708": "Raspberry Pi Camera Module 3",
		"IMX477": "Raspberry Pi HQ Camera",
		"ov5647": "Raspberry Pi Camera Module 1",
		"imx519": "Arducam 16MP (IMX519)",
	}
	for sensor, want := range cases {
		if got := PiCameraModel(sensor); got != want {
			t.Errorf("PiCameraModel(%q) = %q, want %q", sensor, got, want)
		}
	}
	// Unknown sensor falls back to a descriptive name carrying the sensor id.
	if got := PiCameraModel("xyz999"); !strings.Contains(got, "xyz999") {
		t.Errorf("unknown sensor should include the id, got %q", got)
	}
}

func TestCSIName(t *testing.T) {
	got := CSIName("imx708", "0")
	want := "Raspberry Pi Camera Module 3 (imx708) on CSI 0"
	if got != want {
		t.Fatalf("CSIName = %q, want %q", got, want)
	}
}

func TestUSBName(t *testing.T) {
	// V4L2 already gives the product; vendor is prepended if missing.
	if got := USBName("HD Pro Webcam C920", "Logitech, Inc.", "HD Pro Webcam C920"); !strings.Contains(got, "C920") || !strings.HasSuffix(got, "(USB)") {
		t.Fatalf("unexpected USBName: %q", got)
	}
	// Empty V4L2 name falls back to vendor+product.
	if got := USBName("", "Logitech, Inc.", "BRIO"); !strings.Contains(got, "BRIO") {
		t.Fatalf("expected product in name, got %q", got)
	}
}

func TestProfileFor(t *testing.T) {
	csi := ProfileFor(KindCSI)
	if csi.Width != 1280 || csi.Height != 720 {
		t.Fatalf("CSI profile should be 720p, got %dx%d", csi.Width, csi.Height)
	}
	usb := ProfileFor(KindUSB)
	if usb.Width != 1920 || usb.Height != 1080 {
		t.Fatalf("USB profile should be 1080p, got %dx%d", usb.Width, usb.Height)
	}
}

const sampleUSBIDs = `# comment line
046d  Logitech, Inc.
	0892  OrbiCam
	08e5  HD Pro Webcam C920
1d6b  Linux Foundation
	0002  2.0 root hub
`

func TestParseUSBIDs(t *testing.T) {
	v, p := parseUSBIDs(strings.NewReader(sampleUSBIDs), "046d", "08e5")
	if v != "Logitech, Inc." || p != "HD Pro Webcam C920" {
		t.Fatalf("got vendor=%q product=%q", v, p)
	}
	// Vendor known, product unknown → vendor only.
	v, p = parseUSBIDs(strings.NewReader(sampleUSBIDs), "046d", "ffff")
	if v != "Logitech, Inc." || p != "" {
		t.Fatalf("expected vendor only, got vendor=%q product=%q", v, p)
	}
	// Unknown vendor → empty.
	if v, _ := parseUSBIDs(strings.NewReader(sampleUSBIDs), "9999", "0000"); v != "" {
		t.Fatalf("expected empty vendor for unknown id, got %q", v)
	}
}
