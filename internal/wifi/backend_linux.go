//go:build linux

package wifi

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"sportshub/internal/status"
)

// nmcliBackend implements backend using NetworkManager's nmcli tool.
type nmcliBackend struct {
	once      sync.Once
	isSupp    bool   // cached result of supported()
	nmcliPath string // absolute path to nmcli binary
}

func newBackend() backend {
	return &nmcliBackend{}
}

// initOnce initialises the support check exactly once. Callers hold no lock.
func (b *nmcliBackend) initOnce() {
	b.once.Do(func() {
		if !isPi() {
			b.isSupp = false
			return
		}
		p, err := exec.LookPath("nmcli")
		if err != nil {
			log.Printf("[wifi] nmcli not found: %v", err)
			b.isSupp = false
			return
		}
		b.nmcliPath = p
		b.isSupp = true
	})
}

// isPi returns true when /proc/device-tree/model (or /proc/cpuinfo) mentions "Raspberry Pi".
func isPi() bool {
	if b, err := os.ReadFile("/proc/device-tree/model"); err == nil {
		if strings.Contains(strings.ToLower(string(b)), "raspberry pi") {
			return true
		}
	}
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		if strings.Contains(strings.ToLower(string(b)), "raspberry pi") {
			return true
		}
	}
	return false
}

func (b *nmcliBackend) supported() bool {
	b.initOnce()
	return b.isSupp
}

// apMAC reads the wlan0 MAC address from sysfs. Falls back to the first wl* interface.
func (b *nmcliBackend) apMAC() string {
	if addr, err := os.ReadFile("/sys/class/net/wlan0/address"); err == nil {
		return strings.TrimSpace(string(addr))
	}
	// Try any wl* interface.
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "wl") {
			if addr, err := os.ReadFile(filepath.Join("/sys/class/net", e.Name(), "address")); err == nil {
				return strings.TrimSpace(string(addr))
			}
		}
	}
	return ""
}

// nmcli runs the nmcli binary with args, returns trimmed combined output.
func (b *nmcliBackend) nmcli(ctx context.Context, args ...string) (string, error) {
	log.Printf("[wifi] nmcli %v", args)
	out, err := exec.CommandContext(ctx, b.nmcliPath, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// captiveConfDir is the dnsmasq drop-in directory for NetworkManager shared mode.
const captiveConfDir = "/etc/NetworkManager/dnsmasq-shared.d"

// captiveConfPath is the file that redirects all DNS queries to the AP IP.
const captiveConfPath = captiveConfDir + "/sportshub-captive.conf"

// captiveConf is the dnsmasq config that makes every DNS query resolve to the AP IP,
// implementing a captive-portal redirect.
const captiveConf = "address=/#/10.42.0.1\n"

// apPassword is the WPA2 pre-shared key for the AP (minimum 8 characters required by WPA2).
const apPassword = "sportshub"

func (b *nmcliBackend) startAP(ctx context.Context, ssid string) error {
	b.initOnce()
	// Write the captive-portal dnsmasq drop-in. Best-effort — log on error.
	if err := os.MkdirAll(captiveConfDir, 0o755); err != nil {
		log.Printf("[wifi] MkdirAll %s: %v", captiveConfDir, err)
	} else if err := os.WriteFile(captiveConfPath, []byte(captiveConf), 0o644); err != nil {
		log.Printf("[wifi] write captive conf: %v", err)
	}

	// Delete any stale AP connection first; ignore error if it doesn't exist.
	_, _ = b.nmcli(ctx, "connection", "delete", "sportshub-ap")

	// Create the AP connection.
	if _, err := b.nmcli(ctx,
		"connection", "add",
		"type", "wifi",
		"ifname", "wlan0",
		"con-name", "sportshub-ap",
		"autoconnect", "yes",
		"ssid", ssid,
	); err != nil {
		return err
	}

	// Configure it as an AP with shared IPv4 (dnsmasq DHCP + NAT).
	if _, err := b.nmcli(ctx,
		"connection", "modify", "sportshub-ap",
		"802-11-wireless.mode", "ap",
		"802-11-wireless.band", "bg",
		"ipv4.method", "shared",
	); err != nil {
		return err
	}

	// Add WPA2 security with the fixed passphrase.
	if _, err := b.nmcli(ctx,
		"connection", "modify", "sportshub-ap",
		"wifi-sec.key-mgmt", "wpa-psk",
		"wifi-sec.psk", apPassword,
	); err != nil {
		return err
	}

	// Bring the AP up.
	if out, err := b.nmcli(ctx, "connection", "up", "sportshub-ap"); err != nil {
		return fmt.Errorf("nmcli connection up sportshub-ap: %w (output: %s)", err, out)
	}
	return nil
}

func (b *nmcliBackend) stopAP() error {
	b.initOnce()
	ctx := context.Background()
	out, err := b.nmcli(ctx, "connection", "down", "sportshub-ap")
	if err != nil {
		// "not active" is not a real error; just log it.
		if strings.Contains(strings.ToLower(out), "not active") ||
			strings.Contains(strings.ToLower(out), "no active connection") {
			log.Printf("[wifi] stopAP: connection not active (ok)")
			return nil
		}
		return err
	}
	return nil
}

// splitNmcli splits an nmcli terse (-t) line on unescaped colons.
// nmcli escapes literal colons in field values as `\:`.
func splitNmcli(line string) []string {
	var parts []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' && i+1 < len(line) && line[i+1] == ':' {
			cur.WriteByte(':')
			i++ // skip the escaped colon
			continue
		}
		if line[i] == ':' {
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(line[i])
	}
	parts = append(parts, cur.String())
	return parts
}

func (b *nmcliBackend) scan(ctx context.Context) ([]Network, error) {
	b.initOnce()
	// Fields: SSID, SECURITY, SIGNAL, IN-USE
	out, err := b.nmcli(ctx, "-t", "-f", "SSID,SECURITY,SIGNAL,IN-USE",
		"device", "wifi", "list", "--rescan", "yes")
	if err != nil {
		return nil, err
	}

	type candidate struct {
		net    Network
		signal int
	}
	best := map[string]candidate{} // keyed by SSID

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := splitNmcli(line)
		if len(parts) < 4 {
			continue
		}
		ssid := parts[0]
		if ssid == "" {
			continue
		}
		security := parts[1]
		signal, _ := strconv.Atoi(parts[2])
		active := parts[3] == "*"
		secure := security != "" && security != "--"
		n := Network{
			SSID:     ssid,
			Security: security,
			Signal:   signal,
			Secure:   secure,
			Active:   active,
		}
		if prev, ok := best[ssid]; !ok || signal > prev.signal || active {
			best[ssid] = candidate{net: n, signal: signal}
		}
	}

	nets := make([]Network, 0, len(best))
	for _, c := range best {
		nets = append(nets, c.net)
	}
	sort.Slice(nets, func(i, j int) bool {
		return nets[i].Signal > nets[j].Signal
	})
	return nets, nil
}

func (b *nmcliBackend) connect(ctx context.Context, ssid, password string) error {
	b.initOnce()
	var out string
	var err error
	if password == "" {
		out, err = b.nmcli(ctx, "device", "wifi", "connect", ssid, "ifname", "wlan0")
	} else {
		out, err = b.nmcli(ctx, "device", "wifi", "connect", ssid, "password", password, "ifname", "wlan0")
	}
	if err != nil {
		return fmt.Errorf("connect %q: %w (output: %s)", ssid, err, out)
	}
	return nil
}

func (b *nmcliBackend) disconnect(ctx context.Context) error {
	b.initOnce()
	// Find the active 802-11-wireless connection that is not our AP.
	out, err := b.nmcli(ctx, "-t", "-f", "NAME,TYPE,DEVICE", "connection", "show", "--active")
	if err != nil {
		log.Printf("[wifi] disconnect: list active connections: %v", err)
		return nil
	}
	for _, line := range strings.Split(out, "\n") {
		parts := splitNmcli(line)
		if len(parts) < 3 {
			continue
		}
		name, typ := parts[0], parts[1]
		if typ == "802-11-wireless" && name != "sportshub-ap" {
			if _, err := b.nmcli(ctx, "connection", "down", name); err != nil {
				log.Printf("[wifi] disconnect %q: %v", name, err)
			}
			return nil
		}
	}
	log.Printf("[wifi] disconnect: no upstream wifi connection found")
	return nil
}

func (b *nmcliBackend) refresh(ctx context.Context) status.WiFiStatus {
	b.initOnce()
	var s status.WiFiStatus

	// AP active: look for sportshub-ap in active connections.
	if out, err := b.nmcli(ctx, "-t", "-f", "NAME,DEVICE", "connection", "show", "--active"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			parts := splitNmcli(line)
			if len(parts) >= 1 && parts[0] == "sportshub-ap" {
				s.APActive = true
				break
			}
		}
	}

	// Active upstream wifi: find the "yes" line in `device wifi` that isn't the AP SSID.
	if out, err := b.nmcli(ctx, "-t", "-f", "ACTIVE,SSID,SIGNAL", "device", "wifi"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			parts := splitNmcli(line)
			if len(parts) < 3 {
				continue
			}
			if parts[0] == "yes" {
				// This is an active wifi entry.
				ssid := parts[1]
				sig, _ := strconv.Atoi(parts[2])
				s.Connected = true
				s.SSID = ssid
				s.Signal = sig
				break
			}
		}
	}

	// IP address of wlan0.
	if out, err := b.nmcli(ctx, "-t", "-f", "IP4.ADDRESS", "device", "show", "wlan0"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			parts := splitNmcli(line)
			// Output is "IP4.ADDRESS[1]:192.168.1.10/24"
			if len(parts) >= 2 {
				ip := parts[1]
				if idx := strings.Index(ip, "/"); idx != -1 {
					ip = ip[:idx]
				}
				if ip != "" && ip != "--" {
					s.IP = ip
					break
				}
			}
		}
	}

	// Internet: quick TCP dial to 1.1.1.1:53 with 2-second timeout.
	conn, err := net.DialTimeout("tcp", "1.1.1.1:53", 2e9) // 2 seconds
	if err == nil {
		conn.Close()
		s.Internet = true
	}

	return s
}
