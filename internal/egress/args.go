package egress

import (
	"fmt"
	"strings"

	"sportshub2/internal/encode"
)

// isRTMPS reports whether dest is a secure RTMP-over-TLS URL (rtmps://...). The scheme check
// is case-insensitive and tolerant of leading whitespace in the operator-supplied value.
func isRTMPS(dest string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(dest)), "rtmps://")
}

// srtReadURL builds the local SRT pull URL for a published path.
func srtReadURL(srtPort int, streamPath string) string {
	return fmt.Sprintf("srt://127.0.0.1:%d?streamid=read:%s&latency=30000&mode=caller", srtPort, streamPath)
}

// srtPublishURL builds the local SRT publish URL for a preview path.
func srtPublishURL(srtPort int, streamPath string) string {
	return fmt.Sprintf("srt://127.0.0.1:%d?streamid=publish:%s&latency=30000&mode=caller", srtPort, streamPath)
}

// buildGCArgs builds the GameChanger restream ffmpeg command: SRT pull → re-encode (per the
// quality's GameChanger-recommended near-CBR settings) → push to the destination. dest is
// passed through verbatim, so both rtmp:// and rtmps:// destinations are supported.
//
// If previewPath is non-empty, ffmpeg encodes ONCE and tees the result to two outputs: the
// GameChanger destination (FLV) and a local MPEG-TS/SRT publish on previewPath, so the UI can
// preview exactly what's being pushed. The preview slave uses onfail=ignore so a preview hiccup
// can never break the actual broadcast; the GameChanger slave uses the default (abort) so a real
// push failure still surfaces.
//
// The bundled ffmpeg is built with TLS (schannel on Windows, OpenSSL elsewhere), so its native
// RTMP implementation negotiates TLS for rtmps:// over the system trust store with no extra
// flags. A real push to the AWS IVS / live-video.net contribute endpoint completed the TLS +
// RTMP connect/publish exchange with certificate verification at its secure default, so we do
// NOT pass "-tls_verify 0" (it would weaken the push for no benefit). For rtmps we add a 15s
// read/write timeout so a half-open TLS handshake fails fast instead of hanging the push.
func buildGCArgs(streamPath, dest string, enc encode.Params, srtPort int, previewPath string) []string {
	source := srtReadURL(srtPort, streamPath)
	args := []string{
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		// NOTE: never "-avioflags direct" on SRT input — it forces tiny unbuffered reads that
		// libsrt rejects ("buffer size too small for the maximum possible 1316").
		"-probesize", "500000",
		"-analyzeduration", "1000000",
		"-err_detect", "ignore_err",
		"-i", source,
		// Map all input streams explicitly — the tee muxer requires it ("Invalid argument"
		// otherwise). Harmless for the single-output path (one video stream either way).
		"-map", "0",
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
	}

	// For secure rtmps destinations, add a read/write timeout (15s, in microseconds) so a
	// half-open TLS handshake fails fast instead of hanging the push. Output option: precede -f.
	if isRTMPS(dest) {
		args = append(args, "-rw_timeout", "15000000")
	}

	if previewPath != "" {
		tee := "[f=flv]" + dest + "|[f=mpegts:onfail=ignore]" + srtPublishURL(srtPort, previewPath)
		args = append(args, "-f", "tee", tee)
	} else {
		args = append(args, "-f", "flv", dest)
	}
	return args
}
