// Package ffmpeg locates (and on some platforms downloads) the ffmpeg binary
// and parses ffmpeg progress output. ffmpeg is treated as an external managed
// binary rather than a build-time dependency.
package ffmpeg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	dataDirName = "sportshub"
	binDirName  = "bin"
)

// BinDir returns (creating it) the directory where managed binaries live.
// It prefers os.UserCacheDir and falls back to os.TempDir. Works on all OSes.
func BinDir() (string, error) {
	appData, err := os.UserCacheDir()
	if err != nil {
		appData = os.TempDir()
	}
	dir := filepath.Join(appData, dataDirName, binDirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// Path returns the ffmpeg binary path, downloading it if missing (where
// supported). This is the single entry point other packages use to invoke
// ffmpeg without depending on PATH.
func Path() (string, error) {
	return ensureFFmpeg()
}

// ParseFFmpegProgressLine extracts common progress fields from a single ffmpeg
// stderr line. Used by both local ingest and GC restream stderr readers.
func ParseFFmpegProgressLine(line string) (fps float64, bitrate, speed string, frames int) {
	lower := strings.ToLower(line)
	if idx := strings.Index(lower, "frame="); idx >= 0 {
		rest := strings.TrimSpace(line[idx+6:])
		fmt.Sscanf(rest, "%d", &frames)
	}
	if idx := strings.Index(lower, "fps="); idx >= 0 {
		rest := strings.TrimSpace(line[idx+4:])
		fmt.Sscanf(rest, "%f", &fps)
	}
	if idx := strings.Index(lower, "bitrate="); idx >= 0 {
		rest := strings.TrimSpace(line[idx+8:])
		if i := strings.IndexAny(rest, " \t\r\n"); i >= 0 {
			bitrate = rest[:i]
		} else {
			bitrate = rest
		}
	}
	if idx := strings.Index(lower, "speed="); idx >= 0 {
		rest := strings.TrimSpace(line[idx+6:])
		if i := strings.IndexAny(rest, " \t\r\n"); i >= 0 {
			speed = rest[:i]
		} else {
			speed = rest
		}
	}
	return
}
