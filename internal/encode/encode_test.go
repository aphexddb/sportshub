package encode

import "testing"

func TestNormalizeQuality(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"exact 1080p", "1080p", "1080p"},
		{"bare 720", "720", "720p"},
		{"exact 480p", "480p", "480p"},
		{"uppercase 720P", "720P", "720p"},
		{"padded 1080", " 1080 ", "1080p"},
		{"bare 480", "480", "480p"},
		{"uppercase 1080P", "1080P", "1080p"},
		{"label with resolution", "HD 720p", "720p"},
		{"garbage defaults to 1080p", "garbage", "1080p"},
		{"empty defaults to 1080p", "", "1080p"},
		{"whitespace only defaults to 1080p", "   ", "1080p"},
		{"unknown number defaults to 1080p", "360p", "1080p"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeQuality(tt.in); got != tt.want {
				t.Errorf("NormalizeQuality(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParamsFor(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Params
	}{
		{
			name: "480p",
			in:   "480p",
			want: Params{Scale: "854:480", Bitrate: "1500k", MaxRate: "1500k", BufSize: "1500k", Level: "3.0"},
		},
		{
			name: "720p",
			in:   "720p",
			want: Params{Scale: "1280:720", Bitrate: "3500k", MaxRate: "3500k", BufSize: "3500k", Level: "3.1"},
		},
		{
			name: "1080p",
			in:   "1080p",
			want: Params{Scale: "1920:1080", Bitrate: "6000k", MaxRate: "6000k", BufSize: "6000k", Level: "4.1"},
		},
		{
			name: "junk defaults to 1080p",
			in:   "garbage",
			want: Params{Scale: "1920:1080", Bitrate: "6000k", MaxRate: "6000k", BufSize: "6000k", Level: "4.1"},
		},
		{
			name: "empty defaults to 1080p",
			in:   "",
			want: Params{Scale: "1920:1080", Bitrate: "6000k", MaxRate: "6000k", BufSize: "6000k", Level: "4.1"},
		},
		{
			name: "bare 720 selects 720p",
			in:   "720",
			want: Params{Scale: "1280:720", Bitrate: "3500k", MaxRate: "3500k", BufSize: "3500k", Level: "3.1"},
		},
		{
			name: "padded 480 selects 480p",
			in:   " 480 ",
			want: Params{Scale: "854:480", Bitrate: "1500k", MaxRate: "1500k", BufSize: "1500k", Level: "3.0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParamsFor(tt.in)
			if got != tt.want {
				t.Errorf("ParamsFor(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

// TestParamsForCBRInvariant asserts the near-CBR invariant that GameChanger
// relies on: for every quality tier, MaxRate == Bitrate and BufSize == Bitrate.
func TestParamsForCBRInvariant(t *testing.T) {
	qualities := []string{"480p", "720p", "1080p", "garbage", "", "720", " 480 "}
	for _, q := range qualities {
		t.Run(q, func(t *testing.T) {
			p := ParamsFor(q)
			if p.MaxRate != p.Bitrate {
				t.Errorf("ParamsFor(%q): MaxRate %q != Bitrate %q", q, p.MaxRate, p.Bitrate)
			}
			if p.BufSize != p.Bitrate {
				t.Errorf("ParamsFor(%q): BufSize %q != Bitrate %q", q, p.BufSize, p.Bitrate)
			}
		})
	}
}
