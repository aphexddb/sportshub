package ffmpeg

import "testing"

func TestParseFFmpegProgressLine(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantFPS     float64
		wantBitrate string
		wantSpeed   string
		wantFrames  int
	}{
		{
			name:        "full progress line",
			line:        "frame=  123 fps= 30 q=28.0 size=    256kB time=00:00:04.10 bitrate= 511.5kbits/s speed=1.02x",
			wantFPS:     30,
			wantBitrate: "511.5kbits/s",
			wantSpeed:   "1.02x",
			wantFrames:  123,
		},
		{
			name:        "another realistic line",
			line:        "frame= 1500 fps=59.94 q=-1.0 Lsize=   10240kB time=00:00:25.00 bitrate=3355.4kbits/s speed=   1x",
			wantFPS:     59.94,
			wantBitrate: "3355.4kbits/s",
			wantSpeed:   "1x",
			wantFrames:  1500,
		},
		{
			name:        "no progress fields",
			line:        "Input #0, v4l2, from '/dev/video0':",
			wantFPS:     0,
			wantBitrate: "",
			wantSpeed:   "",
			wantFrames:  0,
		},
		{
			name:        "only frame and fps",
			line:        "frame=   42 fps=24",
			wantFPS:     24,
			wantBitrate: "",
			wantSpeed:   "",
			wantFrames:  42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fps, bitrate, speed, frames := ParseFFmpegProgressLine(tt.line)
			if fps != tt.wantFPS {
				t.Errorf("fps = %v, want %v", fps, tt.wantFPS)
			}
			if bitrate != tt.wantBitrate {
				t.Errorf("bitrate = %q, want %q", bitrate, tt.wantBitrate)
			}
			if speed != tt.wantSpeed {
				t.Errorf("speed = %q, want %q", speed, tt.wantSpeed)
			}
			if frames != tt.wantFrames {
				t.Errorf("frames = %v, want %v", frames, tt.wantFrames)
			}
		})
	}
}
