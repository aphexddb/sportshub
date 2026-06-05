// Command sportshub is the entry point: it embeds the viewer's hls.js, constructs the
// application, and runs it. All behaviour lives in internal/app and the service packages.
package main

import (
	_ "embed"
	"log"

	"sportshub2/internal/app"
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
	a := app.New(app.Config{HLSJS: hlsJS, Port: ":8080", Version: version})
	if err := a.Run(); err != nil {
		log.Fatal(err)
	}
}
