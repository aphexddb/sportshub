//go:build !dev

// Package web provides the dashboard's static assets. In a normal build they are embedded
// into the binary (so it runs from any working directory and ships as a single file); build
// with -tags dev to serve them from disk instead for live UI iteration.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Dist returns the embedded dashboard file system (the contents of web/dist).
func Dist() fs.FS {
	f, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err) // dist is embedded at build time; this can't fail in a built binary
	}
	return f
}
