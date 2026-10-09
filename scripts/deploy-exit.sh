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
cp "$ROOT/deploy/exit/install.sh" "$ROOT/deploy/exit/geoirb-awg-exit.service" \
  "$ROOT/deploy/awg-tools.sh" "$ROOT/deploy/99-geoirb-vpn.conf" "$TMP/pkg/"

echo "▶ Installing on $SERVER_USER@$SERVER_HOST:$SERVER_PORT"
# The package (with the private key) goes through stdin, never argv.
tar -C "$TMP/pkg" -cz . | remote "sudo bash -euo pipefail -c '
S=\$(mktemp -d /tmp/geoirb-awg-exit.XXXXXX)
trap \"rm -rf \$S\" EXIT
tar -xz -C \$S
bash \$S/install.sh
'"

echo "▶ Status"
sleep 3
remote "sudo bash -c 'systemctl is-active geoirb-awg-exit.service && awg show awg-exit 2>/dev/null | head -3 || awg show | head -3'"
echo "✔ Exit server ready"
