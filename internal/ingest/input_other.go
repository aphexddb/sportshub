//go:build !windows && !linux && !darwin

package ingest

// captureSpec is a compile-only fallback for unsupported OSes: a synthetic test source so
// the binary builds and runs even where real capture isn't wired up.
func captureSpec(rawID string) InputSpec {
	return InputSpec{
		Format:   "lavfi",
		PreInput: nil,
		Input:    "testsrc=size=1920x1080:rate=30",
	}
}
