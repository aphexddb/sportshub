//go:build dev

package web

import (
	"io/fs"
	"os"
)

// Dist serves web/dist from disk (relative to the working directory) so UI edits show up on
// reload without rebuilding. Enabled by the "dev" build tag; the default build embeds instead.
func Dist() fs.FS {
	return os.DirFS("web/dist")
}
