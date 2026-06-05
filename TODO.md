# TODO

## Done

- [x] **Architecture refactor (DoD / Rob Pike).** Split the ~1,400-line `package main` into focused
  `internal/*` packages with no global state, small interfaces, and unit tests on the isolated
  components (camera state machine, encode, ffmpeg parse, ingest/egress args, sse). Builds on
  windows/linux/macOS × arm64/amd64.
- [x] **Native in-process MediaMTX.** No more downloaded `mediamtx.exe` subprocess — the real
  MediaMTX core runs in-process behind `internal/mediaserver` (pure Go, cross-compiles to all
  targets). ffmpeg stays a managed external binary by design.
- [x] **Init/boot state + loading spinner.** App boots HTTP first, then runs the startup sequence
  (ports → network → ffmpeg → media server) on a goroutine, pushing an `init` state over SSE every
  500ms. The dashboard shows a single spinner with a live status message until `init.done`.
- [x] **Secure RTMPS push.** `rtmps://…:443/…` destinations flow through end-to-end (egress passes
  the dest to ffmpeg verbatim; covered by `internal/egress` arg tests). The GameChanger URL field
  now advertises both `rtmp://` and `rtmps://`.

## Notes / next

- RTMPS was verified end-to-end against the AWS IVS / live-video.net contribute endpoint.
  GameChanger push URLs look like `rtmps://<host>:443/app/<stream-key>?gc_ext=true`.
- NOTE: a real GameChanger stream key was previously committed in this file's history. Treat
  that key as compromised and rotate it in GameChanger. Do not paste live keys into tracked files.
