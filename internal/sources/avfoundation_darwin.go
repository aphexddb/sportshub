//go:build darwin

package sources

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"

	"sportshub2/internal/ffmpeg"
)

// listCamerasImpl is the macOS implementation (called from sources.go).
//
// It enumerates video capture devices via ffmpeg's avfoundation input device by
// running:
//
//	ffmpeg -f avfoundation -list_devices true -i ""
//
// avfoundation prints the device list to stderr in a section like:
//
//	[AVFoundation indev @ ...] AVFoundation video devices:
//	[AVFoundation indev @ ...] [0] FaceTime HD Camera
//	[AVFoundation indev @ ...] [1] Capture screen 0
//	[AVFoundation indev @ ...] AVFoundation audio devices:
//
// The ffmpeg input ID for avfoundation is the device index, so Camera.ID is set
// to the index (e.g. "0") and Camera.Name to the device name. Errors are
// surfaced as a graceful fallback entry rather than a returned error, mirroring
// the Windows implementation.
func listCamerasImpl() ([]Camera, error) {
	ffmpegPath, err := ffmpeg.Path()
	if err != nil {
		return []Camera{{
			ID:   "no-devices",
			Name: "No video devices discovered (avfoundation)",
		}}, nil
	}

	cmd := exec.Command(ffmpegPath, "-f", "avfoundation", "-list_devices", "true", "-i", "")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	// ffmpeg exits non-zero when listing devices (no real input given); ignore the
	// error and parse whatever output was produced.
	_ = cmd.Run()

	cams := parseAVFoundationOutput(out.Bytes())

	if len(cams) == 0 {
		return []Camera{{
			ID:   "no-devices",
			Name: "No video devices discovered (avfoundation)",
		}}, nil
	}

	return cams, nil
}

// parseAVFoundationOutput parses the avfoundation `-list_devices` output and
// returns the video devices found in the "AVFoundation video devices" section.
func parseAVFoundationOutput(output []byte) []Camera {
	var cams []Camera
	scanner := bufio.NewScanner(bytes.NewReader(output))

	inVideoSection := false

	for scanner.Scan() {
		line := scanner.Text()
		lower := strings.ToLower(line)

		if strings.Contains(lower, "avfoundation video devices") {
			inVideoSection = true
			continue
		}
		if strings.Contains(lower, "avfoundation audio devices") {
			// Audio section begins; stop parsing video devices.
			break
		}

		if !inVideoSection {
			continue
		}

		// Lines look like: [AVFoundation indev @ 0x...] [0] FaceTime HD Camera
		// The device index is the SECOND bracketed group; the first is the
		// ffmpeg indev log prefix. Locate it via the "] [" boundary.
		sep := strings.Index(line, "] [")
		if sep == -1 {
			continue
		}
		rest := line[sep+2:] // starts at the "[N] Name" portion

		open := strings.Index(rest, "[")
		close := strings.Index(rest, "]")
		if open == -1 || close == -1 || close <= open {
			continue
		}

		idx := strings.TrimSpace(rest[open+1 : close])
		if !isAllDigits(idx) {
			continue
		}

		name := strings.TrimSpace(rest[close+1:])
		if name == "" {
			continue
		}

		cams = append(cams, Camera{
			ID:   idx,
			Name: name,
		})
	}

	return cams
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}
