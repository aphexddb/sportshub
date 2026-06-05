#!/bin/sh
# Runs before the sportshub package is removed.
set -e

if command -v systemctl >/dev/null 2>&1; then
	systemctl stop sportshub.service || true
	systemctl disable sportshub.service || true
fi
