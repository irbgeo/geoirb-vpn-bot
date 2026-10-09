#!/usr/bin/env bash
# Moves the client peers from the old (GCP) Amnezia container to the RU
# server: one ssh session to the old host (secret/exit-access.yaml) fetches
# its awg0.conf, one ssh session to the RU server (secret/server-access.yaml)
# runs the deployed /opt/geoirb-vpn-bot/import-peers.sh. The conf holds PSKs:
# it travels through stdout/stdin and a 0700 temp dir, never argv or the screen.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
umask 077

echo "▶ Fetching the old conf"
(
  export ACCESS_FILE="${OLD_ACCESS_FILE:-$ROOT/secret/exit-access.yaml}"
  source "$ROOT/scripts/lib.sh"
  remote 'sudo bash -c "for c in amnezia-awg2 amnezia-awg; do docker exec \$c cat /opt/amnezia/awg/awg0.conf 2>/dev/null && exit 0; done; exit 1"'
) >"$TMP/old.conf"
grep -q '^\[Peer\]' "$TMP/old.conf" || { echo "error: no peers in the old conf" >&2; exit 1; }

echo "▶ Importing on the RU server"
(
  source "$ROOT/scripts/lib.sh"
  remote 'sudo bash -c "umask 077; f=\$(mktemp); trap \"rm -f \$f\" EXIT; cat >\$f; bash /opt/geoirb-vpn-bot/import-peers.sh \$f"' <"$TMP/old.conf"
)
