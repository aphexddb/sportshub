//go:build windows

package media

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Windows-specific paths and download logic for the spike

const (
	dataDirName = "sportshub"
	binDirName  = "bin"
)

// GetBinDir returns the directory where we store MediaMTX and ffmpeg on Windows
func GetBinDir() (string, error) {
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

// EnsureMediaMTX downloads mediamtx.exe if not present (Windows build)
func EnsureMediaMTX() (string, error) {
	binDir, err := GetBinDir()
	if err != nil {
		return "", err
	}

	path := filepath.Join(binDir, "mediamtx.exe")
	if _, err := os.Stat(path); err == nil {
		reportInitProgress("binaries", "MediaMTX already present")
		return path, nil // already have it
	}

	// Use a recent stable release (update this URL when new versions come out)
	url := "https://github.com/bluenviron/mediamtx/releases/download/v1.11.3/mediamtx_v1.11.3_windows_amd64.zip"

	reportInitProgress("binaries", "Downloading MediaMTX (~25MB, first run only)…")
	fmt.Println("Downloading MediaMTX (one-time, ~25MB)...")
	zipPath := filepath.Join(binDir, "mediamtx.zip")
	if err := downloadFile(url, zipPath); err != nil {
		return "", fmt.Errorf("failed to download MediaMTX: %w", err)
	}

	if err := extractExeFromZip(zipPath, "mediamtx.exe", path); err != nil {
		return "", err
	}
	os.Remove(zipPath)

	fmt.Println("MediaMTX downloaded to", path)
	return path, nil
}

// GetFFmpegPath returns the path to the ffmpeg binary we manage (downloads it if missing).
// This is the preferred way for all code to invoke ffmpeg so we don't depend on PATH.
func GetFFmpegPath() (string, error) {
	return EnsureFFmpeg()
}

// EnsureFFmpeg downloads a static ffmpeg.exe if not present
func EnsureFFmpeg() (string, error) {
	binDir, err := GetBinDir()
	if err != nil {
		return "", err
	}

	path := filepath.Join(binDir, "ffmpeg.exe")
	if _, err := os.Stat(path); err == nil {
		reportInitProgress("binaries", "ffmpeg already present")
		return path, nil
	}

	// Using a reliable full build from gyan.dev (widely used, kept up to date)
	url := "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-full.7z"

	// For simplicity in the spike we use the zip version from BtbN (smaller + direct exe)
	// If you want the absolute latest, we can switch.
	url = "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-gpl.zip"

	reportInitProgress("binaries", "Downloading ffmpeg (~100MB, first run only)…")
	fmt.Println("Downloading ffmpeg (one-time, ~100MB+ — this will take a minute)...")
	zipPath := filepath.Join(binDir, "ffmpeg.zip")
	if err := downloadFile(url, zipPath); err != nil {
		return "", fmt.Errorf("failed to download ffmpeg: %w", err)
	}

	// The BtbN zip has ffmpeg-master-latest-win64-gpl/bin/ffmpeg.exe inside
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

func extractExeFromZip(zipPath, exeName, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if strings.HasSuffix(f.Name, exeName) && !f.FileInfo().IsDir() {
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
