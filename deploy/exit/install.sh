#!/usr/bin/env bash
# Runs as root on the exit server from the unpacked package (scripts/deploy-exit.sh).
# Installs awg tools, the tunnel conf (its PostUp runs exit-fw.sh: forwarding/NAT) and the
# geoirb-awg-exit service. Never touches docker or other services. ROOT is a
# path prefix for tests.
set -euo pipefail
S="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="${ROOT:-}"

modprobe amneziawg || { echo "error: kernel module amneziawg is missing" >&2; exit 1; }
bash "$S/awg-tools.sh"

# The conf's PostUp/PostDown call it; in place before the tunnel starts.
install -d "$ROOT/usr/local/sbin"
install -m 755 "$S/exit-fw.sh" "$ROOT/usr/local/sbin/geoirb-exit-fw.sh"

# The tunnel is restarted only when its conf changed (a run that changes
# nothing must not cut foreign traffic). Stop first: `down` must run the OLD
# conf's PostDown, before the conf is replaced.
if ! cmp -s "$S/awg-exit.conf" "$ROOT/etc/geoirb-vpn/awg-exit.conf"; then
  systemctl stop geoirb-awg-exit.service 2>/dev/null || true
  install -d -m 700 "$ROOT/etc/geoirb-vpn"
  install -m 600 "$S/awg-exit.conf" "$ROOT/etc/geoirb-vpn/awg-exit.conf"
fi
install -d "$ROOT/etc/systemd/system" "$ROOT/etc/sysctl.d" "$ROOT/etc/modules-load.d" "$ROOT/etc/modprobe.d"
install -m 644 "$S/geoirb-awg-exit.service" "$ROOT/etc/systemd/system/"
install -m 644 "$S/99-geoirb-vpn.conf" "$ROOT/etc/sysctl.d/"
# conntrack must be loaded before systemd-sysctl at boot, or the two
# nf_conntrack settings fall back to the defaults after a reboot; the
# modprobe file sets the hash size at the next load of the module. Warnings
# only: the tunnel is stopped here and must come back up.
install -m 644 "$S/nf_conntrack-modules.conf" "$ROOT/etc/modules-load.d/nf_conntrack.conf"
install -m 644 "$S/nf_conntrack-modprobe.conf" "$ROOT/etc/modprobe.d/nf_conntrack.conf"
modprobe nf_conntrack || echo "warning: module nf_conntrack not loaded" >&2
sysctl -q -p "$ROOT/etc/sysctl.d/99-geoirb-vpn.conf" || echo "warning: sysctl settings not applied" >&2

systemctl daemon-reload
systemctl enable --quiet geoirb-awg-exit.service
systemctl start geoirb-awg-exit.service
