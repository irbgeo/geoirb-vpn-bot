#!/usr/bin/env bash
# Split routing of VPN clients on the RU server (geoirb-vpn-routes.service,
# start and reload): the nft table (deploy/geoirb-vpn.nft), the last good RU
# set, the fwmark rule and the fail-closed `unreachable` route of table 100.
# Every step is idempotent and nothing is deleted, so a reload has no window.
# ROOT is a path prefix for tests.
set -euo pipefail
ROOT="${ROOT:-}"
SAVED="$ROOT/var/lib/geoirb-vpn-bot/ru4.nft"

nft -f "$ROOT/etc/geoirb-vpn/geoirb-vpn.nft"
# The RU set from the last good ru-nets run: an empty set after a reboot
# would send RU traffic through the exit until the timer runs. A bad file
# never blocks the rest; the next ru-nets run refills the set.
if [[ -s "$SAVED" ]]; then
  nft -f "$SAVED" || echo "warning: saved RU set $SAVED not loaded" >&2
fi
ip route replace unreachable default metric 4096 table 100
# `ip rule add` of an existing rule fails (EEXIST): add only when missing
# (grep without -q: an early exit could fail the pipe under pipefail).
if ! ip rule show prio 110 | grep 'fwmark 0x1 lookup 100' >/dev/null; then
  ip rule add fwmark 0x1 lookup 100 prio 110
fi
