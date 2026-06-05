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
