//go:build windows

package ffmpeg

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ensureFFmpeg downloads a static ffmpeg.exe into BinDir if not already present
// and returns its path.
func ensureFFmpeg() (string, error) {
	binDir, err := BinDir()
	if err != nil {
		return "", err
	}

	path := filepath.Join(binDir, "ffmpeg.exe")
	if _, err := os.Stat(path); err == nil {
		return path, nil // already have it
	}

	// Arch-aware static GPL build from BtbN (smaller + direct exe inside zip).
	var url string
	switch runtime.GOARCH {
	case "arm64":
		url = "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-winarm64-gpl.zip"
	default:
		url = "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-gpl.zip"
	}

	fmt.Println("Downloading ffmpeg (one-time, ~100MB+ — this will take a minute)...")
	zipPath := filepath.Join(binDir, "ffmpeg.zip")
	if err := downloadFile(url, zipPath); err != nil {
		return "", fmt.Errorf("failed to download ffmpeg: %w", err)
	}

	// The BtbN zip has ffmpeg-master-latest-*-gpl/bin/ffmpeg.exe inside.
	if err := extractExeFromZipNested(zipPath, "ffmpeg.exe", path); err != nil {
		return "", err
	}
	os.Remove(zipPath)

	fmt.Println("ffmpeg downloaded to", path)
	return path, nil
}

func downloadFile(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func extractExeFromZipNested(zipPath, exeName, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if strings.HasSuffix(f.Name, "/bin/"+exeName) || strings.HasSuffix(f.Name, "\\bin\\"+exeName) {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()

			out, err := os.Create(dest)
			if err != nil {
				return err
			}
			defer out.Close()

			_, err = io.Copy(out, rc)
			return err
		}
	}
	return fmt.Errorf("%s not found in zip", exeName)
}
