#!/bin/sh
# Runs before the sportshub package is removed.
set -e

if command -v systemctl >/dev/null 2>&1; then
	systemctl stop sportshub.service || true
	systemctl disable sportshub.service || true
fi

# Clean up Wi-Fi AP configuration so removal doesn't leave the Pi's networking altered.
if command -v nmcli >/dev/null 2>&1; then
	nmcli connection down sportshub-ap 2>/dev/null || true
	nmcli connection delete sportshub-ap 2>/dev/null || true
	rm -f /etc/NetworkManager/dnsmasq-shared.d/sportshub-captive.conf 2>/dev/null || true
fi
