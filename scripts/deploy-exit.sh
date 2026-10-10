#!/usr/bin/env bash
# Installs the exit side of the RU <-> exit tunnel on the host in
# secret/exit-access.yaml (host, port, user, password). Creates
# secret/tunnel.yaml on first run. Two ssh connections: install, then status.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export ACCESS_FILE="$ROOT/secret/exit-access.yaml"
source "$ROOT/scripts/lib.sh"

"$ROOT/scripts/tunnel-keys.sh"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -m 700 "$TMP/pkg"
(umask 077 && "$ROOT/scripts/render-tunnel.sh" exit "$SERVER_HOST" >"$TMP/pkg/awg-exit.conf")
cp "$ROOT/deploy/exit/install.sh" "$ROOT/deploy/exit/geoirb-awg-exit.service" "$ROOT/deploy/exit/exit-fw.sh" \
  "$ROOT/deploy/awg-tools.sh" "$ROOT/deploy/99-geoirb-vpn.conf" \
  "$ROOT/deploy/nf_conntrack-modules.conf" "$ROOT/deploy/nf_conntrack-modprobe.conf" "$TMP/pkg/"

echo "▶ Installing on $SERVER_USER@$SERVER_HOST:$SERVER_PORT"
# One ssh login (the server throttles quick repeated ones). The package, with
# the private key, goes through stdin, never argv. The last lines print the
# status; a service that is not active or an interface that is missing fails.
tar -C "$TMP/pkg" -cz . | remote "sudo bash -euo pipefail -c '
S=\$(mktemp -d /tmp/geoirb-awg-exit.XXXXXX)
trap \"rm -rf \$S\" EXIT
tar -xz -C \$S
bash \$S/install.sh
sleep 3
st=\$(systemctl is-active geoirb-awg-exit.service || true)
echo \"geoirb-awg-exit: \$st\"
awg show awg-exit | head -3
[ \"\$st\" = active ] && awg show awg-exit >/dev/null
'"
echo "✔ Exit server ready"
