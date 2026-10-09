#!/usr/bin/env bash
# Runs as root on the exit server from the unpacked package (scripts/deploy-exit.sh).
# Installs awg tools, the tunnel conf (its PostUp does forwarding/NAT) and the
# geoirb-awg-exit service. Never touches docker or other services. ROOT is a
# path prefix for tests.
set -euo pipefail
S="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="${ROOT:-}"

modprobe amneziawg || { echo "error: kernel module amneziawg is missing" >&2; exit 1; }
bash "$S/awg-tools.sh"

# Stop first: `down` must run the OLD conf's PostDown, before the conf is replaced.
systemctl stop geoirb-awg-exit.service 2>/dev/null || true

install -d -m 700 "$ROOT/etc/geoirb-vpn"
install -m 600 "$S/awg-exit.conf" "$ROOT/etc/geoirb-vpn/awg-exit.conf"
install -d "$ROOT/etc/systemd/system" "$ROOT/etc/sysctl.d"
install -m 644 "$S/geoirb-awg-exit.service" "$ROOT/etc/systemd/system/"
install -m 644 "$S/99-geoirb-vpn.conf" "$ROOT/etc/sysctl.d/"
sysctl -q -p "$ROOT/etc/sysctl.d/99-geoirb-vpn.conf" || echo "warning: sysctl settings not applied" >&2

systemctl daemon-reload
systemctl enable --quiet geoirb-awg-exit.service
systemctl start geoirb-awg-exit.service
