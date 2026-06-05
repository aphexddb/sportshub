//go:build linux

package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"sportshub2/internal/devices"
)

// listCamerasImpl enumerates real cameras on Linux. On a Raspberry Pi the /dev/video* space is
// dominated by ISP/codec/CSI pipeline nodes (rp1-cfe, pispbe, *-hevc-dec, fe_*), which are NOT
// usable cameras — the actual CSI sensor (e.g. an Arducam imx708) is owned by libcamera. So we
// list libcamera (CSI) cameras via rpicam, plus genuine USB/UVC capture devices, and skip the
// platform pipeline nodes entirely. Each camera is named from open hardware data.
func listCamerasImpl() ([]Camera, error) {
	cams := append(libcameraCameras(), usbCameras()...)
	if len(cams) == 0 {
		return []Camera{{ID: "no-devices", Name: "No cameras found (CSI via libcamera or USB)", Kind: string(devices.KindStub)}}, nil
	}
	return cams, nil
}

// rpicamHeader matches a camera line from `rpicam-hello --list-cameras`, e.g.:
//
//	0 : imx708 [4608x2592 10-bit] (/base/axi/.../imx708@1a)
var rpicamHeader = regexp.MustCompile(`^\s*(\d+)\s*:\s*(\S+)`)

// libcameraCameras parses `rpicam-hello --list-cameras` for CSI/libcamera cameras and names
// them from the sensor model (imx708 -> "Raspberry Pi Camera Module 3"). Each is captured (in
// the ingest layer) via rpicam-vid, so its ID carries the "libcamera:" scheme.
func libcameraCameras() []Camera {
	bin := firstInPath("rpicam-hello", "libcamera-hello")
	if bin == "" {
		return nil
	}
	out, _ := exec.Command(bin, "--list-cameras").CombinedOutput()
	var cams []Camera
	for _, ln := range strings.Split(string(out), "\n") {
		m := rpicamHeader.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		idx, sensor := m[1], m[2]
		cams = append(cams, Camera{
			ID:       "libcamera:" + idx,
			Name:     devices.CSIName(sensor, idx),
			Kind:     string(devices.KindCSI),
			Model:    sensor,
			Vendor:   "Raspberry Pi",
			Location: "CSI " + idx,
		})
	}
	return cams
}

// usbCameras lists genuine USB/UVC capture devices, identified by their sysfs device path
// resolving through "/usb". This excludes every Raspberry Pi platform pipeline node. A single
// webcam exposes several video nodes; we keep the first per USB interface and name it from the
// V4L2 product string plus the vendor/product resolved from the open usb.ids database.
func usbCameras() []Camera {
	devs, _ := filepath.Glob("/dev/video*")
	sort.Slice(devs, func(i, j int) bool { return videoIndex(devs[i]) < videoIndex(devs[j]) })
	seen := map[string]bool{}
	var cams []Camera
	for _, dev := range devs {
		base := filepath.Base(dev)
		real, err := filepath.EvalSymlinks("/sys/class/video4linux/" + base + "/device")
		if err != nil || !strings.Contains(real, "/usb") {
			continue // not a USB camera (CSI/ISP/codec platform node)
		}
		if seen[real] {
			continue // another node of a webcam we already listed
		}
		seen[real] = true

		v4l2Name := readSysName(base)
		vid, pid := readUSBID(real)
		vendor, product := devices.LookupUSB(vid, pid)
		cams = append(cams, Camera{
			ID:       dev,
			Name:     devices.USBName(v4l2Name, vendor, product),
			Kind:     string(devices.KindUSB),
			Model:    pickNonEmpty(product, v4l2Name),
			Vendor:   vendor,
			Location: "USB",
		})
	}
	return cams
}

// readUSBID reads idVendor/idProduct for a USB video node. real is the resolved sysfs device
// path (the USB interface, e.g. .../usb1/1-1/1-1:1.0); the ids live on the parent device dir.
func readUSBID(real string) (vid, pid string) {
	dir := filepath.Dir(real) // interface -> device (.../1-1)
	for i := 0; i < 3 && dir != "/" && dir != "."; i++ {
		v, errV := os.ReadFile(filepath.Join(dir, "idVendor"))
		p, errP := os.ReadFile(filepath.Join(dir, "idProduct"))
		if errV == nil && errP == nil {
			return strings.TrimSpace(string(v)), strings.TrimSpace(string(p))
		}
		dir = filepath.Dir(dir)
	}
	return "", ""
}

func readSysName(base string) string {
	if b, err := os.ReadFile("/sys/class/video4linux/" + base + "/name"); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	return base
}

func pickNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func firstInPath(names ...string) string {
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// videoIndex extracts the numeric suffix of /dev/videoN for natural ordering (video2 < video19).
func videoIndex(dev string) int {
	n := 0
	for _, r := range filepath.Base(dev) {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
		}
	}
	return n
}
