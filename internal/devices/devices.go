// Package devices is sportshub's hardware-aware camera configuration. It maps the raw camera
// identifiers each OS reports (a Pi CSI sensor, a USB vendor/product id, a DirectShow name)
// to human-readable names using open hardware data, and provides the capture profile to use
// for each kind of device. This is the single place that knows "what hardware am I dealing
// with, where is it, and how should I capture from it".
package devices

import (
	"bufio"
	"io"
	"strings"
)

// parseUSBIDs resolves vendor and product names for the given 4-hex-digit ids from a reader
// over the open usb.ids database format (vendor lines at column 0, product lines tab-indented).
// Pure and OS-independent so it can be unit tested; file access lives in the per-OS LookupUSB.
func parseUSBIDs(r io.Reader, vid, pid string) (vendor, product string) {
	vid = strings.ToLower(strings.TrimSpace(vid))
	pid = strings.ToLower(strings.TrimSpace(pid))
	if len(vid) != 4 {
		return "", ""
	}
	sc := bufio.NewScanner(r)
	inVendor := false
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line[0] != '\t' { // vendor line: "VVVV  Name"
			if len(line) > 6 && strings.EqualFold(line[:4], vid) {
				vendor = strings.TrimSpace(line[4:])
				inVendor = true
				if pid == "" {
					return vendor, ""
				}
			} else if inVendor {
				break // past our vendor's block
			}
		} else if inVendor { // product line: "\tPPPP  Name"
			t := strings.TrimLeft(line, "\t")
			if len(t) > 6 && strings.EqualFold(t[:4], pid) {
				return vendor, strings.TrimSpace(t[4:])
			}
		}
	}
	return vendor, product
}

// Kind is the class of camera, which determines how we capture from it and its default
// capture profile.
type Kind string

const (
	KindCSI          Kind = "csi"          // Raspberry Pi / libcamera CSI sensor (rpicam-vid)
	KindUSB          Kind = "usb"          // USB/UVC webcam (V4L2 on Linux)
	KindDShow        Kind = "dshow"        // Windows DirectShow device
	KindAVFoundation Kind = "avfoundation" // macOS avfoundation device
	KindStub         Kind = "stub"         // unsupported platform fallback
)

// CaptureProfile is the resolution/framerate/bitrate to capture at. Defaults vary by kind:
// the Pi CSI path re-encodes on the CPU (no HW H.264 on Pi 5) so it favours 720p; USB/desktop
// devices default to 1080p.
type CaptureProfile struct {
	Width, Height, FPS int
	Bitrate            string // ffmpeg -b:v value, e.g. "4000k"
}

// ProfileFor returns the default capture profile for a device kind.
func ProfileFor(k Kind) CaptureProfile {
	switch k {
	case KindCSI:
		// Pi 5 has no hardware H.264 encoder; 720p keeps the libx264 encode comfortably
		// real-time and the stream low-latency.
		return CaptureProfile{Width: 1280, Height: 720, FPS: 30, Bitrate: "4000k"}
	default:
		return CaptureProfile{Width: 1920, Height: 1080, FPS: 30, Bitrate: "6000k"}
	}
}

// piSensorModels maps a CSI image-sensor model to the human product name of the camera that
// uses it. Open data from the Raspberry Pi and Arducam camera documentation. libcamera can
// only report the sensor, not the module, so this is a best-effort friendly mapping.
var piSensorModels = map[string]string{
	"imx708": "Raspberry Pi Camera Module 3",
	"imx219": "Raspberry Pi Camera Module 2",
	"imx477": "Raspberry Pi HQ Camera",
	"imx296": "Raspberry Pi Global Shutter Camera",
	"imx500": "Raspberry Pi AI Camera",
	"ov5647": "Raspberry Pi Camera Module 1",
	"imx519": "Arducam 16MP (IMX519)",
	"imx462": "Arducam (IMX462)",
	"imx327": "Arducam (IMX327)",
	"imx290": "Arducam (IMX290)",
	"ov9281": "Arducam Global Shutter (OV9281)",
}

// PiCameraModel returns the human product name for a CSI sensor, falling back to the raw
// sensor name when unknown.
func PiCameraModel(sensor string) string {
	if m, ok := piSensorModels[strings.ToLower(strings.TrimSpace(sensor))]; ok {
		return m
	}
	return "Raspberry Pi Camera (" + sensor + ")"
}

// CSIName builds the friendly name for a CSI camera, e.g.
// "Raspberry Pi Camera Module 3 (imx708) on CSI 0".
func CSIName(sensor, slot string) string {
	name := PiCameraModel(sensor)
	if sensor != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(sensor)) {
		name += " (" + sensor + ")"
	}
	if slot != "" {
		name += " on CSI " + slot
	}
	return name
}

// USBName builds the friendly name for a USB camera from its V4L2 product string and the
// vendor/product resolved from the open usb.ids database. e.g.
// "Logitech HD Pro Webcam C920 (USB)".
func USBName(v4l2Name, vendor, product string) string {
	base := strings.TrimSpace(v4l2Name)
	// V4L2 names sometimes carry a ": <node>" suffix or trailing bus info; keep the head.
	if i := strings.Index(base, ":"); i > 0 {
		base = strings.TrimSpace(base[:i])
	}
	switch {
	case base == "" && product != "":
		base = strings.TrimSpace(vendor + " " + product)
	case base == "":
		base = "USB Camera"
	case vendor != "" && !strings.Contains(strings.ToLower(base), strings.ToLower(firstWord(vendor))):
		base = firstWord(vendor) + " " + base
	}
	return base + " (USB)"
}

func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}
