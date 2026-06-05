//go:build linux

package sources

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// listCamerasImpl is the Linux implementation (called from sources.go).
//
// It enumerates Video4Linux2 capture devices by globbing /dev/video*. For each
// device Camera.ID is the device path (e.g. "/dev/video0"). The human-readable
// name is taken from /sys/class/video4linux/<base>/name when readable, otherwise
// it falls back to the device path. ffmpeg is not required for enumeration.
func listCamerasImpl() ([]Camera, error) {
	paths, err := filepath.Glob("/dev/video*")
	if err != nil {
		return []Camera{{
			ID:   "no-devices",
			Name: "No /dev/video* devices found",
		}}, nil
	}

	sort.Strings(paths)

	var cams []Camera
	for _, p := range paths {
		name := p
		base := filepath.Base(p)
		if data, rerr := os.ReadFile("/sys/class/video4linux/" + base + "/name"); rerr == nil {
			if trimmed := strings.TrimSpace(string(data)); trimmed != "" {
				name = trimmed
			}
		}
		cams = append(cams, Camera{
			ID:   p,
			Name: name,
		})
	}

	if len(cams) == 0 {
		return []Camera{{
			ID:   "no-devices",
			Name: "No /dev/video* devices found",
		}}, nil
	}

	return cams, nil
}
