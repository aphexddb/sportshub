# Wi-Fi Access Point — Operator Guide

Sportshub can turn a Raspberry Pi into a Wi-Fi access point so phones and laptops can reach
the dashboard without a pre-existing network.  The Pi broadcasts an AP, serves the dashboard
as a captive portal, and can bridge internet from an upstream Wi-Fi network via NAT.

## What it does

1. Sportshub creates a NetworkManager connection named `sportshub-ap` in **shared** mode
   (`ipv4.method shared`).  NetworkManager handles DHCP and NAT automatically — no manual
   `iptables` rules needed.
2. The AP SSID is `sportshub-<last4 of the Wi-Fi MAC>` (e.g. `sportshub-A3F2`).
   Default WPA2 passphrase: **`sportshub`**.
3. Clients land on subnet `10.42.0.0/24`; the Pi's AP gateway is `10.42.0.1`.
4. The sportshub dashboard is reachable at **http://10.42.0.1/** and is served as a captive
   portal — most phones redirect there automatically after connecting.
5. From the dashboard → **Use Wifi** panel: pick an upstream network, enter the password, and
   click Connect.  NetworkManager bridges internet to AP clients via the same shared-mode NAT.

## Requirements

- **Raspberry Pi OS Bookworm** (or later) — NetworkManager is the default network backend.
- `network-manager` package installed (pulled in automatically as a package dependency).
- Sportshub runs as **root** (the default systemd unit); `nmcli`, port 80, and writing
  `/etc/NetworkManager/dnsmasq-shared.d/sportshub-captive.conf` all require root.

## Single-radio caveat

The Pi's onboard Wi-Fi radio doing AP + station (STA) simultaneously is a best-effort mode.
It works in light conditions but throughput and reliability are limited:

- The AP and upstream association share the same channel; heavy upstream traffic can cause
  AP client drops.
- For reliable concurrent AP + internet bridging, a **second Wi-Fi interface** (USB dongle)
  is recommended — sportshub will prefer it for the upstream STA connection automatically
  when present.

## Quick-start

1. Install the `.deb` on a Pi — the postinstall script enables NetworkManager.
2. Power on, wait ~15 seconds for the AP to appear.
3. On a phone or laptop, connect to `sportshub-XXXX` (password `sportshub`).
4. The captive portal redirects to the dashboard (or navigate to http://10.42.0.1/).
5. Scroll to **Use Wifi**, select your upstream network, enter the password, tap **Connect**.

## Networking reference

| Item | Value |
|------|-------|
| AP SSID | `sportshub-<last4 of MAC>` |
| WPA2 passphrase | `sportshub` |
| AP gateway / captive portal | `10.42.0.1` |
| DHCP subnet | `10.42.0.0/24` |
| NM connection name | `sportshub-ap` |
| Captive-portal DNS drop-in | `/etc/NetworkManager/dnsmasq-shared.d/sportshub-captive.conf` |

## Troubleshooting

```sh
# Sportshub service logs
journalctl -u sportshub -f

# All NetworkManager connections (look for sportshub-ap)
nmcli connection show

# Available Wi-Fi networks seen by the Pi
nmcli device wifi list

# Bring the AP down / up manually
nmcli connection down sportshub-ap
nmcli connection up sportshub-ap
```
