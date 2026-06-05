package egress

import (
	"fmt"

	"sportshub2/internal/encode"
)

// srtReadURL builds the local SRT pull URL for a published path.
func srtReadURL(srtPort int, streamPath string) string {
	return fmt.Sprintf("srt://127.0.0.1:%d?streamid=read:%s&latency=30000&mode=caller", srtPort, streamPath)
}

// buildGCArgs builds the GameChanger restream ffmpeg command: SRT pull → re-encode (per the
// quality's GameChanger-recommended near-CBR settings) → FLV to the destination. dest is
// passed through verbatim, so both rtmp:// and rtmps:// destinations are supported.
func buildGCArgs(streamPath, dest string, enc encode.Params, srtPort int) []string {
	source := srtReadURL(srtPort, streamPath)
	return []string{
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		// NOTE: never "-avioflags direct" on SRT input — it forces tiny unbuffered reads that
		// libsrt rejects ("buffer size too small for the maximum possible 1316").
		"-probesize", "500000",
		"-analyzeduration", "1000000",
		"-err_detect", "ignore_err",
		"-i", source,
		"-vf", "scale=" + enc.Scale,
		"-r", "30",
		"-c:v", "libx264",
		"-preset", "faster",
		"-profile:v", "high",
		"-level", enc.Level,
		"-b:v", enc.Bitrate,
		"-maxrate", enc.MaxRate,
		"-bufsize", enc.BufSize,
		"-g", "60",
		"-keyint_min", "60",
		"-sc_threshold", "0",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-profile:a", "aac_low",
		"-b:a", "160k",
		"-ar", "48000",
		"-ac", "2",
		"-f", "flv",
		dest,
	}
}
