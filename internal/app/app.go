// Package app wires the isolated services (media server, ingest, egress, camera state
// machine, SSE hub) into a running HTTP application. It owns no streaming logic itself —
// it constructs the pieces, injects their dependencies, and exposes the HTTP/SSE contract
// the web UI consumes. There is no package-level mutable state; everything hangs off App.
package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sportshub2/internal/camera"
	"sportshub2/internal/egress"
	"sportshub2/internal/ffmpeg"
	"sportshub2/internal/ingest"
	"sportshub2/internal/mediaserver"
	"sportshub2/internal/netutil"
	"sportshub2/internal/proc"
	"sportshub2/internal/sse"
	"sportshub2/internal/status"
)

// Config holds the externally-provided inputs to the app.
type Config struct {
	HLSJS []byte // embedded hls.min.js served at /static/hls.min.js
	Port  string // listen address for the dashboard/API, e.g. ":8080"
}

// App is the running application: services + HTTP wiring.
type App struct {
	cfg   Config
	media *mediaserver.Server
	ing   *ingest.Ingest
	egr   *egress.Egress
	cams  *camera.Manager
	hub   *sse.Hub
	boot  *bootState

	addrs mediaserver.Addrs
	host  string // non-loopback LAN IP for phone/QR access
}

// New constructs the app and wires every dependency. Nothing is started yet; call Run.
func New(cfg Config) *App {
	if cfg.Port == "" {
		cfg.Port = ":8080"
	}
	a := &App{cfg: cfg}

	a.media = mediaserver.New("", mediaserver.DefaultAddrs())
	a.addrs = a.media.Addrs()
	a.hub = sse.New()
	a.boot = newBootState("ports", "network", "binaries", "media")

	// ingest publishes into the media server's SRT port; its callbacks drive SSE broadcasts
	// and report unexpected capture exits to the state machine.
	a.ing = ingest.New(a.addrs.SRT,
		func() { go a.broadcast() },
		func(rawID string) { a.cams.HandleIngestExit(rawID) },
	)
	a.egr = egress.New(a.addrs.SRT)

	// The state machine depends only on interfaces, satisfied by the concrete services above.
	a.cams = camera.NewManager(a.media, a.ing, a.egr, func() { go a.broadcast() })

	return a
}

// Run starts HTTP FIRST so the dashboard (and its loading spinner) loads instantly, then runs
// the boot sequence on a background goroutine, broadcasting init progress over SSE. It blocks
// serving until the process exits.
func (a *App) Run() error {
	// Kill any leftover instance first so it can't hold our HTTP port; this is cheap (no
	// port scanning / sleep). The fuller media-port cleanup happens during boot().
	proc.KillStale()
	a.host = netutil.PublicHost() // fast; needed before the page builds URLs

	srv := &http.Server{Addr: a.cfg.Port, Handler: a.routes()}

	// Own the shutdown signal. The in-process MediaMTX core installs its OWN SIGINT/SIGTERM
	// handler, and Go delivers a signal to every registered handler — so without our own
	// handler, Ctrl+C shuts MediaMTX down but leaves our HTTP server (and the process)
	// running. We catch it too, tear down captures + media, then close the server so
	// ListenAndServe returns and the process exits.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		s := <-sigCh
		log.Printf("[shutdown] %v received — stopping", s)
		_ = a.Close()
		_ = srv.Close()
	}()

	log.Printf("=== SportsHub ===")
	log.Printf("Open http://%s%s (use this address from phones on the same LAN) or http://localhost%s", a.host, a.cfg.Port, a.cfg.Port)

	go a.runBoot()

	// Light ticker so live stats keep flowing to the UI even during quiet ffmpeg periods.
	go func() {
		t := time.NewTicker(1500 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			if len(a.ing.Active()) > 0 {
				a.broadcast()
			}
		}
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// runBoot runs the startup sequence, updating the init state and broadcasting it (also every
// 500ms via a ticker) so the UI shows a single spinner with a live status message until ready.
func (a *App) runBoot() {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				a.broadcast()
			}
		}
	}()
	defer func() {
		close(stop)
		a.broadcast() // final state (Done)
	}()

	// 1. Free stale ports from a prior crashed instance (including a leftover external
	//    mediamtx from before the in-process migration, and our own API port).
	a.boot.start("ports", "Freeing ports…")
	proc.Cleanup([]int{a.addrs.RTMP, a.addrs.SRT, a.addrs.WebRTC, a.addrs.HLS, a.addrs.API})
	a.boot.ok("ports")
	a.broadcast()

	// 2. Detect the LAN host (re-confirm; cheap).
	a.boot.start("network", "Detecting network…")
	a.host = netutil.PublicHost()
	a.boot.ok("network")
	a.broadcast()

	// 3. Ensure ffmpeg is available (downloads it on Windows the first time; PATH lookup elsewhere).
	//    Surface the downloader's fine-grained progress into the spinner message live.
	a.boot.start("binaries", "Checking media tools (ffmpeg)…")
	ffmpeg.SetProgress(func(msg string) {
		a.boot.start("binaries", msg)
		a.broadcast()
	})
	if _, err := ffmpeg.Path(); err != nil {
		a.boot.fail("binaries", err.Error())
		log.Printf("[boot] ffmpeg not ready: %v", err)
	} else {
		a.boot.ok("binaries")
	}
	ffmpeg.SetProgress(nil)
	a.broadcast()

	// 4. Start the in-process MediaMTX media server.
	a.boot.start("media", "Starting media server…")
	if err := a.media.Start(context.Background()); err != nil {
		a.boot.fail("media", err.Error())
		log.Printf("[boot] media server failed: %v", err)
	} else {
		a.boot.ok("media")
	}

	a.boot.finish()
	log.Printf("[boot] startup complete")
}

// Close tears everything down: stop all camera captures / GC pushes (killing their ffmpeg
// children) then stop the in-process media server. Safe to call once.
func (a *App) Close() error {
	a.cams.StopAll()
	return a.media.Close()
}

// buildSnapshot assembles the full status view pushed over SSE / returned by /api/status views.
func (a *App) buildSnapshot() status.Snapshot {
	snap := status.Snapshot{Ts: time.Now()}
	snap.Init = a.boot.snapshot()
	// Always emit a (possibly empty) array, never null — the client's Array.isArray guard
	// rejects null and would show stale state when the last device stops.
	snap.Devices = []status.DeviceStatus{}

	snap.Global.MediaMTXReady = a.media.IsReady()
	snap.Global.ActiveIngests = len(a.ing.Active())
	snap.Global.GCQuality = a.cams.Quality()

	devs, g := a.cams.Snapshot()
	snap.Devices = devs
	snap.Global.GCActive = g.Active
	snap.Global.GCPath = g.Path
	snap.Global.GCActiveRaw = g.RawID
	return snap
}

func (a *App) broadcast() { a.hub.Broadcast(a.buildSnapshot()) }
