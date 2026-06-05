// Package netutil provides small network helpers for LAN host detection.
package netutil

import "net"

// PublicHost returns a non-loopback IPv4 address suitable for phone/LAN access
// (e.g. viewer URLs and QR codes). It iterates over the host's interfaces,
// skipping those that are down or loopback, and returns the first usable IPv4
// address found. If none is found it returns "127.0.0.1".
func PublicHost() string {
	publicHost := "127.0.0.1"
	ifaces, err := net.Interfaces()
	if err != nil {
		return publicHost
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || (iface.Flags&net.FlagLoopback) != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return publicHost
}
