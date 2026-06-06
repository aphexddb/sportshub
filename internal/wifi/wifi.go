// Package wifi manages a Raspberry Pi Wi-Fi Access Point and upstream Wi-Fi internet
// connection via NetworkManager's nmcli. It is safe for concurrent use.
//
// Typical usage:
//
//	m := wifi.New(func() { /* notify UI */ })
//	if m.Supported() {
//	    go func() {
//	        for range time.Tick(30 * time.Second) {
//	            m.Refresh(ctx)
//	        }
//	    }()
//	}
package wifi

import (
	"context"
	"strings"
	"sync"

	"sportshub/internal/status"
)

// apIP is the gateway IP for the AP captive portal.
const apIP = "10.42.0.1"

// Network is a Wi-Fi access point visible during a scan.
type Network struct {
	SSID     string `json:"ssid"`
	Security string `json:"security"`
	Signal   int    `json:"signal"`
	Secure   bool   `json:"secure"`
	Active   bool   `json:"active"`
}

// backend is implemented by the platform-specific file (backend_linux.go / backend_other.go).
// Every method must be safe to call concurrently.
type backend interface {
	supported() bool
	apMAC() string // Wi-Fi MAC address for SSID suffix; "" if unknown
	startAP(ctx context.Context, ssid string) error
	stopAP() error
	scan(ctx context.Context) ([]Network, error)
	connect(ctx context.Context, ssid, password string) error
	disconnect(ctx context.Context) error
	// refresh gathers lightweight status (no full scan) and returns it.
	// The Manager fills in Supported and APSSID before caching.
	refresh(ctx context.Context) status.WiFiStatus
}

// Manager controls the Wi-Fi AP and upstream connection. Create with New.
type Manager struct {
	mu       sync.Mutex
	cached   status.WiFiStatus
	apSSID   string
	onChange func()
	b        backend
}

// New creates a Manager. onChange is called after the cached status changes; it may be nil.
func New(onChange func()) *Manager {
	b := newBackend()
	apSSID := "sportshub-" + Last4Hex(b.apMAC())
	m := &Manager{
		b:        b,
		apSSID:   apSSID,
		onChange: onChange,
	}
	m.cached = status.WiFiStatus{
		Supported: b.supported(),
		APSSID:    apSSID,
	}
	return m
}

// Supported reports whether this host is a Raspberry Pi with nmcli available.
func (m *Manager) Supported() bool {
	return m.b.supported()
}

// StartAP brings up the Wi-Fi access point.
func (m *Manager) StartAP(ctx context.Context) error {
	if err := m.b.startAP(ctx, m.apSSID); err != nil {
		return err
	}
	m.Refresh(ctx)
	return nil
}

// StopAP tears down the Wi-Fi access point.
func (m *Manager) StopAP() error {
	err := m.b.stopAP()
	m.Refresh(context.Background())
	return err
}

// Scan performs a full Wi-Fi scan (slow) and returns visible networks.
func (m *Manager) Scan(ctx context.Context) ([]Network, error) {
	return m.b.scan(ctx)
}

// Connect joins a Wi-Fi network. Pass an empty password for open networks.
func (m *Manager) Connect(ctx context.Context, ssid, password string) error {
	if err := m.b.connect(ctx, ssid, password); err != nil {
		return err
	}
	m.Refresh(ctx)
	return nil
}

// Disconnect drops the current upstream Wi-Fi connection.
func (m *Manager) Disconnect(ctx context.Context) error {
	if err := m.b.disconnect(ctx); err != nil {
		return err
	}
	m.Refresh(ctx)
	return nil
}

// Status returns the last cached WiFiStatus. It is cheap and never blocks.
func (m *Manager) Status() status.WiFiStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.cached
	// Always ensure these fields reflect current state.
	s.Supported = m.b.supported()
	s.APSSID = m.apSSID
	return s
}

// Refresh updates the cached status with a lightweight nmcli query (no scan).
// Call periodically; also called automatically after AP/connection changes.
func (m *Manager) Refresh(ctx context.Context) {
	s := m.b.refresh(ctx)
	s.Supported = m.b.supported()
	s.APSSID = m.apSSID

	// On a single-radio Pi the AP appears in NetworkManager's active-Wi-Fi list under its
	// own SSID. That is the access point itself, not an upstream internet connection, so
	// don't report it as one (the IP would just be the AP gateway).
	if s.SSID == m.apSSID {
		s.Connected = false
		s.SSID = ""
		s.Signal = 0
		if s.IP == apIP {
			s.IP = ""
		}
	}

	m.mu.Lock()
	changed := s != m.cached
	m.cached = s
	cb := m.onChange
	m.mu.Unlock()

	if changed && cb != nil {
		cb()
	}
}

// APIP returns the AP gateway IP used for captive portal redirection ("10.42.0.1").
func (m *Manager) APIP() string {
	return apIP
}

// APActive reports whether the AP is currently active (cheap, uses cached status).
func (m *Manager) APActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cached.APActive
}

// Last4Hex strips ':' and '-' separators from a MAC address, lowercases it, and returns
// the last 4 hex characters. Returns "0000" if the stripped string has fewer than 4 chars.
func Last4Hex(mac string) string {
	s := strings.ToLower(strings.NewReplacer(":", "", "-", "").Replace(mac))
	if len(s) < 4 {
		return "0000"
	}
	return s[len(s)-4:]
}
