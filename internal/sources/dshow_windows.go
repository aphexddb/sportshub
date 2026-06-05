//go:build windows

package sources

import (
	"bufio"
	"bytes"
	"log"
	"os/exec"
	"strings"

	"sportshub2/internal/devices"
	"sportshub2/internal/ffmpeg"
)

// listCamerasImpl is the Windows implementation (called from sources.go)
func listCamerasImpl() ([]Camera, error) {
	ffmpegPath, err := ffmpeg.Path()
	if err != nil {
		return []Camera{{
			ID:   "ffmpeg-missing",
			Name: "Failed to download ffmpeg (needed for camera listing and RTMP ingest)",
		}}, nil
	}

	cmd := exec.Command(ffmpegPath, "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err = cmd.Run()

	output := out.Bytes()

	// Always log the raw output for debugging (very useful while getting dshow working)
	log.Printf("=== ffmpeg -list_devices output (len=%d) ===", len(output))
	log.Printf("%s", string(output))
	log.Printf("=== end of ffmpeg device list ===")

	// If ffmpeg failed hard with almost no output, surface it
	if err != nil && len(output) < 50 {
		log.Printf("ffmpeg device listing failed: %v", err)
		return []Camera{{
			ID:   "ffmpeg-missing",
			Name: "ffmpeg failed to list devices (see console above for details)",
		}}, nil
	}

	return parseDSHOWOutput(output), nil
}

// parseDSHOWOutput handles both old and new ffmpeg dshow output formats.
// Newer builds (like the one we download) output lines like:
//
//	[in#0 @ ...] "Logitech BRIO" (video)
func parseDSHOWOutput(output []byte) []Camera {
	var cams []Camera
	scanner := bufio.NewScanner(bytes.NewReader(output))

	inVideoSection := false

	for scanner.Scan() {
		line := scanner.Text()
		lower := strings.ToLower(line)

		// Old-style header detection (still useful for some builds)
		if strings.Contains(lower, "directshow video devices") {
			inVideoSection = true
			continue
		}
		if strings.Contains(lower, "directshow audio devices") {
			inVideoSection = false
			continue
		}

		// Newer ffmpeg git builds (2025-2026) use this format for device listing:
		// [in#0 @ ...] "Device Name" (video)
		// We treat any line containing " (video)" as a video device.
		if strings.Contains(lower, " (video)") && strings.Contains(line, `"`) {
			start := strings.Index(line, `"`)
			end := strings.LastIndex(line, `"`)
			if start != -1 && end != -1 && end > start {
				name := strings.TrimSpace(line[start+1 : end])
				if name != "" && !strings.HasPrefix(strings.ToLower(name), "dummy") {
					lowerName := strings.ToLower(name)
					// WDM devices (e.g. "Mevo Wireless Camera (WDM)") are virtual wrappers
					// and cannot be used for actual streaming/capture in this context.
					if strings.Contains(lowerName, "wdm") {
						continue
					}
					cams = append(cams, Camera{
						ID:       "video=" + name,
						Name:     name,
						Kind:     string(devices.KindDShow),
						Model:    name,
						Location: "DirectShow",
					})
				}
			}
			continue
		}

		// Old style: only parse quoted names when we're inside the video section
		if inVideoSection && strings.Contains(line, `"`) {
			start := strings.Index(line, `"`)
			end := strings.LastIndex(line, `"`)
			if start != -1 && end != -1 && end > start {
				name := strings.TrimSpace(line[start+1 : end])
				if name != "" && !strings.HasPrefix(strings.ToLower(name), "dummy") {
					lowerName := strings.ToLower(name)
					if strings.Contains(lowerName, "wdm") {
						continue
					}
					cams = append(cams, Camera{
						ID:       "video=" + name,
						Name:     name,
						Kind:     string(devices.KindDShow),
						Model:    name,
						Location: "DirectShow",
					})
				}
			}
		}
	}

	// Only use fallback if we truly found zero real devices
	if len(cams) == 0 {
		cams = append(cams, Camera{
			ID:   "no-devices",
			Name: "No video devices discovered by ffmpeg dshow (WDM devices are filtered)",
		})
	}

	return cams
}
