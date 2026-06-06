// Command sportshub is the entry point: it embeds the viewer's hls.js, constructs the
// application, and runs it. All behaviour lives in internal/app and the service packages.
package main

import (
	_ "embed"
	"log"
	"runtime"

	"sportshub/internal/app"
)

//go:embed static/hls.min.js
var hlsJS []byte

// Build metadata, stamped at release time via -ldflags (see .goreleaser.yaml). Defaults are
// used for `go build`/`go run` so a dev build is clearly labelled.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	log.Printf("sportshub %s (commit %s, built %s)", version, commit, date)

	// Serve on the standard HTTP port (80) on Linux/macOS so the dashboard is reachable at a
	// bare http://<host>/. On Windows, where binding 80 commonly collides with IIS/other
	// services and needs elevation, keep 8080. (Port 80 needs root/admin on Linux — the
	// Raspberry Pi systemd unit runs as root, so that's covered.)
	port := ":80"
	if runtime.GOOS == "windows" {
		port = ":8080"
	}

	a := app.New(app.Config{HLSJS: hlsJS, Port: port, Version: version})
	if err := a.Run(); err != nil {
		log.Fatal(err)
	}
}
