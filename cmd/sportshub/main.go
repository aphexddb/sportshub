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

func main() {
	a := app.New(app.Config{HLSJS: hlsJS, Port: ":8080"})
	if err := a.Run(); err != nil {
		log.Fatal(err)
	}
}
