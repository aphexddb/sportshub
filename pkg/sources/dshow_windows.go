//go:build windows

package sources

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"
)

// listCamerasImpl is the Windows implementation (called from sources.go)
func listCamerasImpl() ([]Camera, error) {
	cmd := exec.Command("ffmpeg", "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()

	output := out.Bytes()

	// If ffmpeg itself is not found or failed hard, return a clear signal
	if err != nil && len(output) < 50 {
		return []Camera{{
			ID:   "ffmpeg-missing",
			Name: "ffmpeg not found in PATH — install it for server-side RTMP listing",
		}}, nil
	}

	return parseDSHOWOutput(output), nil
}

// parseDSHOWOutput is more tolerant of different ffmpeg dshow output formats
func parseDSHOWOutput(output []byte) []Camera {
	var cams []Camera
	scanner := bufio.NewScanner(bytes.NewReader(output))

	inVideoSection := false

	for scanner.Scan() {
		line := scanner.Text()

		lower := strings.ToLower(line)

		if strings.Contains(lower, "directshow video devices") {
			inVideoSection = true
			continue
		}
		if strings.Contains(lower, "directshow audio devices") {
			inVideoSection = false
			continue
		}

		if !inVideoSection {
			continue
		}

		// Common patterns:
		// "Device Name" (video)
		// "Device Name"
		// [dshow @ ...] "Device Name" (video)
		if strings.Contains(line, `"`) {
			// Extract first quoted string
			start := strings.Index(line, `"`)
			end := strings.LastIndex(line, `"`)
			if start != -1 && end != -1 && end > start {
				name := strings.TrimSpace(line[start+1 : end])
				if name != "" && !strings.HasPrefix(strings.ToLower(name), "dummy") {
					cams = append(cams, Camera{
						ID:   "video=" + name,
						Name: name,
					})
				}
			}
		}
	}

	// Only use fallback if we truly found zero real devices
	if len(cams) == 0 {
		cams = append(cams, Camera{
			ID:   "video=Mevo Start",
			Name: "No devices found via ffmpeg dshow (is ffmpeg in PATH?)",
		})
	}

	return cams
}
