#!/bin/sh
# Runs after the sportshub package is installed/upgraded (deb/rpm/apk).
set -e

if command -v systemctl >/dev/null 2>&1; then
	systemctl daemon-reload || true
	systemctl enable sportshub.service || true
	# Start (or restart on upgrade) so it's live immediately and on every boot.
	systemctl restart sportshub.service || true
fi

echo "sportshub installed. It serves the dashboard on http://<this-host>:8080"
echo "  status: sudo systemctl status sportshub"
echo "  logs:   journalctl -u sportshub -f"
echo "Requires ffmpeg (installed as a dependency). On a Raspberry Pi with a CSI camera,"
echo "rpicam-apps must also be present (libcamera/rpicam-vid)."

# Raspberry Pi — Wi-Fi Access Point / captive-portal setup (best-effort, never fails install).
if grep -qi "Raspberry Pi" /proc/device-tree/model 2>/dev/null; then
	echo ""
	echo "Raspberry Pi detected. Enabling NetworkManager for the Wi-Fi Access Point feature..."
	systemctl enable --now NetworkManager 2>/dev/null || true
	echo "  Wi-Fi AP feature: sportshub will broadcast an AP named sportshub-XXXX (last 4 of MAC)."
	echo "  Connect a phone or laptop to that AP — the captive portal opens the dashboard."
	echo "  From the dashboard you can join an upstream Wi-Fi network for internet bridging."
fi
