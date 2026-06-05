package ingest

import (
	"strings"
	"testing"
)

func argString(args []string) string { return strings.Join(args, " ") }

func TestSrtPublishURL(t *testing.T) {
	got := srtPublishURL(8890, "cam0")
	want := "srt://127.0.0.1:8890?streamid=publish:cam0&latency=30000&mode=caller"
	if got != want {
		t.Fatalf("srtPublishURL = %q, want %q", got, want)
	}
}

func TestBuildIngestArgs_Shape(t *testing.T) {
	in := InputSpec{Format: "dshow", PreInput: []string{"-rtbufsize", "200M"}, Input: "video=Cam:audio=Microphone (Cam)"}
	args := buildIngestArgs(in, 8890, "cam0")
	s := argString(args)

	// Format flag first.
	if args[0] != "-f" || args[1] != "dshow" {
		t.Fatalf("expected leading -f dshow, got %v", args[:2])
	}
	// PreInput flags present before -i.
	iIdx := indexOf(args, "-i")
	rtIdx := indexOf(args, "-rtbufsize")
	if rtIdx < 0 || iIdx < 0 || rtIdx > iIdx {
		t.Fatalf("-rtbufsize must appear before -i: rt=%d i=%d", rtIdx, iIdx)
	}
	// Input value follows -i.
	if args[iIdx+1] != in.Input {
		t.Fatalf("input after -i = %q, want %q", args[iIdx+1], in.Input)
	}
	// Encode + output tail.
	for _, want := range []string{"libx264", "-tune zerolatency", "-c:a aac", "-f mpegts"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in args: %s", want, s)
		}
	}
	// Last arg is the SRT publish URL.
	last := args[len(args)-1]
	if !strings.HasPrefix(last, "srt://127.0.0.1:8890?streamid=publish:cam0") {
		t.Fatalf("last arg should be the SRT publish URL, got %q", last)
	}
}

func indexOf(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
}
