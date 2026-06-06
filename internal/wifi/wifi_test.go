package wifi_test

import (
	"testing"

	"sportshub/internal/wifi"
)

func TestLast4Hex(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"dc:a6:32:ab:cd:ef", "cdef"},
		{"B8-27-EB-12-34-56", "3456"},
		{"", "0000"},
		{"abc", "0000"}, // fewer than 4 hex chars after stripping
		{"00:11:22:33:44:55", "4455"},
		{"AA:BB:CC:DD:EE:FF", "eeff"},
	}
	for _, tc := range tests {
		got := wifi.Last4Hex(tc.input)
		if got != tc.want {
			t.Errorf("Last4Hex(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestNew_NonPi verifies that on a non-Raspberry Pi host (or any non-Linux host),
// New returns a Manager that reports Supported()==false and an SSID of "sportshub-0000"
// (since apMAC() returns "" on stubs and non-Pi Linux systems).
func TestNew_NonPi(t *testing.T) {
	m := wifi.New(nil)
	if m.Supported() {
		// This test may legitimately pass when run on a Raspberry Pi with nmcli.
		t.Log("Supported() is true — test running on a Raspberry Pi with nmcli; skipping assertion")
		return
	}
	s := m.Status()
	if s.Supported {
		t.Error("Status().Supported should be false on non-Pi/non-Linux host")
	}
	if s.APSSID != "sportshub-0000" {
		t.Errorf("Status().APSSID = %q, want %q", s.APSSID, "sportshub-0000")
	}
	if m.APIP() != "10.42.0.1" {
		t.Errorf("APIP() = %q, want %q", m.APIP(), "10.42.0.1")
	}
}
