// Package encode holds the GameChanger encoder-settings logic.
//
// It is pure logic with no IO: given a desired output quality, it produces the
// ffmpeg/H.264 encoding parameters GameChanger recommends for reliable
// streaming. The settings deliberately mimic OBS in CBR (constant bitrate)
// mode, which is what GameChanger's streaming guidance recommends.
package encode

import "strings"

// Params holds the H.264 encoding parameters for a single output quality.
//
// The MaxRate and BufSize fields are intentionally set equal to Bitrate to
// produce a near-CBR (constant bitrate) stream. GameChanger recommends OBS in
// CBR mode, and matching maxrate==bv together with bufsize==bv approximates
// that behavior with ffmpeg's rate control: the encoder is held close to the
// target bitrate rather than allowed to spike, which keeps the stream stable.
type Params struct {
	// Scale is the ffmpeg scale filter target, e.g. "1920:1080".
	Scale string
	// Bitrate is the target video bitrate (ffmpeg -b:v).
	Bitrate string
	// MaxRate is the maximum bitrate; set equal to Bitrate for near-CBR.
	MaxRate string
	// BufSize is the rate-control buffer size; set equal to Bitrate for near-CBR.
	BufSize string
	// Level is the H.264 level.
	Level string
}

// ParamsFor returns the GameChanger encoding parameters for the given quality.
//
// The quality string is normalized via NormalizeQuality, so any input that
// resolves to a known tier selects that tier's settings; anything else defaults
// to 1080p.
//
// Tiers:
//   - 480p:  854x480,   ~1500k, H.264 level 3.0
//   - 720p:  1280x720,  ~3500k, H.264 level 3.1
//   - 1080p: 1920x1080, ~6000k, H.264 level 4.1 (default)
//
// For every tier MaxRate and BufSize equal Bitrate to mimic OBS CBR.
func ParamsFor(quality string) Params {
	switch NormalizeQuality(quality) {
	case "480p":
		// 480p: light bitrate for low-bandwidth uploads, level 3.0.
		return Params{Scale: "854:480", Bitrate: "1500k", MaxRate: "1500k", BufSize: "1500k", Level: "3.0"}
	case "720p":
		// 720p: balanced bitrate for typical connections, level 3.1.
		return Params{Scale: "1280:720", Bitrate: "3500k", MaxRate: "3500k", BufSize: "3500k", Level: "3.1"}
	}
	// 1080p: full HD default, level 4.1.
	return Params{Scale: "1920:1080", Bitrate: "6000k", MaxRate: "6000k", BufSize: "6000k", Level: "4.1"}
}

// NormalizeQuality coerces an arbitrary quality string to one of the supported
// tiers: "480p", "720p", or "1080p".
//
// Matching is case-insensitive and tolerant of surrounding whitespace and
// labels (e.g. "720", "720P", " 1080 " all match). Anything that does not
// contain a recognized resolution defaults to "1080p".
func NormalizeQuality(q string) string {
	s := strings.ToLower(strings.TrimSpace(q))
	if strings.Contains(s, "480") {
		return "480p"
	}
	if strings.Contains(s, "720") {
		return "720p"
	}
	return "1080p"
}
