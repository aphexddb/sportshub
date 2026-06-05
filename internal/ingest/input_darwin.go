//go:build darwin

package ingest

// captureSpec builds the avfoundation input for a macOS capture device. rawID is the
// device index reported by internal/sources (e.g. "0"). Video-only here; avfoundation
// audio is a separate device and is left out of the spike capture.
func captureSpec(rawID string) InputSpec {
	if rawID == "" {
		rawID = "0"
	}
	return InputSpec{
		Format:   "avfoundation",
		PreInput: []string{"-framerate", "30", "-video_size", "1920x1080"},
		Input:    rawID,
	}
}
