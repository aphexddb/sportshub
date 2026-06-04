# GameChanger Encoder Settings — Validated Reference

**Status:** Validated live against a real GameChanger (Amazon IVS) ingest endpoint on
2026-06-04 — full pipeline (BRIO → MediaMTX → re-encode → RTMP push) connected, streamed
~25s with `drop=0` at `speed≈1.04x`, and `Output #0, flv` opened with no errors.

This file is a **known-good snapshot** of the GameChanger push encoder config so it can be
restored if it's lost during refactoring. It is NOT the source of truth — the live code is.
As of this writing the config lives in:

- `cmd/sportshub/main.go` → `gcParamsForQuality()` (per-quality bitrate/scale/level)
- `cmd/sportshub/camera.go` → the ffmpeg args builder (encoder flags)

## Why these values

GameChanger ingests RTMP and **redelivers to viewers over HLS (~10–20s latency)**. So the
push must be optimized for *quality-per-bit and HLS-friendliness*, NOT low latency:

- **No `-tune zerolatency`** on the GC push — it disables B-frames/lookahead and costs
  quality for latency the HLS pipeline negates anyway. (The *local* preview ingest in
  `pkg/media/ingest.go` keeps its low-latency tuning — that's correct for the ~1s preview.)
- **2s GOP (`-g 60 -keyint_min 60` at 30fps)** — the RTMP→HLS ingest standard; aligns
  segment boundaries. `-sc_threshold 0` keeps keyframe cadence fixed.
- **Near-CBR** (`maxrate == bufsize == bv`) — what RTMP ingest expects; mirrors OBS "CBR".
- **`-r 30`** locks output framerate so the 2s GOP is deterministic.
- **Hard rule:** GameChanger will not broadcast above **1080p**.

## Per-quality params (gcParamsForQuality)

| Quality | scale       | bv     | maxrate | bufsize | level |
|---------|-------------|--------|---------|---------|-------|
| 1080p   | `1920:1080` | `6000k`| `6000k` | `6000k` | `4.1` |
| 720p    | `1280:720`  | `3500k`| `3500k` | `3500k` | `3.1` |
| 480p    | `854:480`   | `1500k`| `1500k` | `1500k` | `3.0` |

`normalizeQuality()` maps any input containing "480"/"720" to those tiers; everything else
falls back to 1080p.

## Full ffmpeg args for the GameChanger push

```
-fflags nobuffer
-flags low_delay
# (never "-avioflags direct" on SRT input — libsrt rejects the tiny reads)
-probesize 500000
-analyzeduration 1000000
-err_detect ignore_err
-i <srt source>
-vf scale=<enc.scale>
-r 30
-c:v libx264
-preset faster
-profile:v high
-level <enc.level>
-b:v <enc.bv>
-maxrate <enc.maxrate>
-bufsize <enc.bufsize>
-g 60
-keyint_min 60
-sc_threshold 0
-pix_fmt yuv420p
-c:a aac
-profile:a aac_low
-b:a 160k
-ar 48000
-ac 2
-f flv
<dest>
```

## Live verification (encoder headers observed)

```
Video: h264, profile High, level 4.1, yuv420p, 1920x1080, 6000 kb/s
bitrate max/min/avg: 6000000/0/6000000  buffer size: 6000000   (CBR)
Audio: aac (LC), 48000 Hz, stereo, 160 kb/s
30 fps
```
