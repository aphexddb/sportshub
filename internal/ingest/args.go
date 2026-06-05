package ingest

import "fmt"

// InputSpec is the OS-specific capture front-end for ffmpeg: the input format (dshow /
// avfoundation / v4l2), the format-specific flags that must precede -i, and the -i value.
// The encode/output tail is identical across platforms, which keeps arg building testable.
type InputSpec struct {
	Format   string   // ffmpeg -f value: "dshow" / "avfoundation" / "v4l2"
	PreInput []string // format-specific flags before -i (e.g. -rtbufsize, -video_size, -framerate)
	Input    string   // the -i argument (device spec)
}

// srtPublishURL builds the local SRT publish URL for a path. latency is in microseconds;
// loopback is lossless so a small 30ms buffer is plenty and avoids needless delay.
func srtPublishURL(srtPort int, streamPath string) string {
	return fmt.Sprintf("srt://127.0.0.1:%d?streamid=publish:%s&latency=30000&mode=caller", srtPort, streamPath)
}

// buildIngestArgs assembles the full ffmpeg command for a capture: OS-specific input +
// a low-latency H.264/AAC encode published into MediaMTX over SRT (MPEG-TS).
//
// Capture is 1080p30 because GameChanger wants high-quality 1080p for the final push.
// -err_detect ignore_err helps with common EOI issues on USB webcams.
func buildIngestArgs(in InputSpec, srtPort int, streamPath string) []string {
	args := []string{"-f", in.Format}
	args = append(args, in.PreInput...)
	args = append(args,
		"-use_wallclock_as_timestamps", "1",
		"-err_detect", "ignore_err",
		"-fflags", "nobuffer",
		"-i", in.Input,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-b:v", "5000k",
		"-maxrate", "6000k",
		"-bufsize", "8000k",
		"-g", "15", // lower GOP for reduced latency
		"-keyint_min", "15",
		"-pix_fmt", "yuv420p",
		"-fflags", "+genpts",
		"-c:a", "aac",
		"-b:a", "128k",
		"-ar", "48000",
		"-f", "mpegts",
		srtPublishURL(srtPort, streamPath),
	)
	return args
}
