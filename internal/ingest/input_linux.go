//go:build linux

package ingest

// captureSpec builds the V4L2 input for a Linux capture device. rawID is the device path
// reported by internal/sources (e.g. "/dev/video0").
func captureSpec(rawID string) InputSpec {
	if rawID == "" {
		rawID = "/dev/video0"
	}
	return InputSpec{
		Format:   "v4l2",
		PreInput: []string{"-framerate", "30", "-video_size", "1920x1080"},
		Input:    rawID,
	}
}
