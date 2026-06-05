package egress

import (
	"strings"
	"testing"

	"sportshub2/internal/encode"
	"sportshub2/internal/status"
)

func TestSrtReadURL(t *testing.T) {
	got := srtReadURL(8890, "cam1")
	want := "srt://127.0.0.1:8890?streamid=read:cam1&latency=30000&mode=caller"
	if got != want {
		t.Fatalf("srtReadURL = %q, want %q", got, want)
	}
}

func TestBuildGCArgs_QualityAndPassthrough(t *testing.T) {
	enc := encode.ParamsFor("720p")
	args := buildGCArgs("cam0", "rtmp://ingest.example/app/key", enc, 8890)

	// Source is the SRT read URL.
	iIdx := indexOf(args, "-i")
	if iIdx < 0 || !strings.HasPrefix(args[iIdx+1], "srt://127.0.0.1:8890?streamid=read:cam0") {
		t.Fatalf("expected SRT read source after -i, got %v", args)
	}
	// Encode params wired through.
	assertPairs(t, args, map[string]string{
		"-vf":      "scale=" + enc.Scale,
		"-b:v":     enc.Bitrate,
		"-maxrate": enc.MaxRate,
		"-bufsize": enc.BufSize,
		"-level":   enc.Level,
	})
	// Output format flv, dest is the final arg, verbatim.
	if args[len(args)-1] != "rtmp://ingest.example/app/key" {
		t.Fatalf("dest must be the final arg verbatim, got %q", args[len(args)-1])
	}
}

func TestBuildGCArgs_RTMPSPassthrough(t *testing.T) {
	enc := encode.ParamsFor("1080p")
	dest := "rtmps://601c62c19c9e.global-contribute.live-video.net:443/app/sk_secret?gc_ext=true"
	args := buildGCArgs("cam0", dest, enc, 8890)

	// Secure RTMPS destination must pass through unchanged as the final ffmpeg arg.
	if args[len(args)-1] != dest {
		t.Fatalf("rtmps dest must pass through verbatim, got %q", args[len(args)-1])
	}
	// And we still output FLV (ffmpeg negotiates TLS for rtmps:// transparently).
	fIdx := lastIndexOf(args, "-f")
	if fIdx < 0 || args[fIdx+1] != "flv" {
		t.Fatalf("expected -f flv output, got %v", args)
	}
}

func TestUpdateStats_MergesNonZero(t *testing.T) {
	s := &status.StreamStats{}
	// First chunk sets frame/fps.
	if !updateStats(s, "frame=  10 fps= 30 q=28.0 size=1kB time=00:00:00.33 bitrate= 24.0kbits/s speed=1x") {
		t.Fatal("expected stats to change on first progress line")
	}
	if s.Frames != 10 || s.FPS != 30 || s.Bitrate != "24.0kbits/s" {
		t.Fatalf("unexpected stats: %+v", *s)
	}
	// A line with no progress fields should not change.
	if updateStats(s, "some random ffmpeg log line") {
		t.Fatal("non-progress line should not change stats")
	}
	// Later frame count overwrites; missing fields retain previous value.
	updateStats(s, "frame=  50 speed=1.01x")
	if s.Frames != 50 || s.FPS != 30 {
		t.Fatalf("merge should keep last non-zero fps, got %+v", *s)
	}
}

// helpers

func indexOf(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
}

func lastIndexOf(args []string, s string) int {
	idx := -1
	for i, a := range args {
		if a == s {
			idx = i
		}
	}
	return idx
}

func assertPairs(t *testing.T, args []string, want map[string]string) {
	t.Helper()
	for flag, val := range want {
		i := indexOf(args, flag)
		if i < 0 || i+1 >= len(args) {
			t.Fatalf("flag %s not found", flag)
		}
		if args[i+1] != val {
			t.Fatalf("%s = %q, want %q", flag, args[i+1], val)
		}
	}
}
