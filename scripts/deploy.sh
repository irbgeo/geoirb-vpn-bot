#!/usr/bin/env bash
# Builds the bot and installs (or updates) it on the RU VPN server in
# secret/server-access.yaml: the bot as the systemd service geoirb-vpn-bot,
# host AmneziaWG (awg0 for clients, the awg-exit tunnel to the exit server in
# secret/exit-access.yaml), split routing, DNS for clients and the daily
# backup timer. Safe to re-run: the first run also creates the vpnbot user,
# awg0.conf and the directories. One ssh connection (the server throttles
# many quick password logins).
#
# Server layout:
#   /opt/geoirb-vpn-bot/bot, *.sh        binary, backup and RU networks scripts
#   /etc/geoirb-vpn-bot/env              config (640 root:vpnbot), from scripts/server-env.sh
#   /etc/amnezia/amneziawg/awg0.conf     client VPN (vpnbot 600), created once
#   /etc/geoirb-vpn/                     awg-exit.conf (tunnel), geoirb-vpn.nft
#   /var/lib/geoirb-vpn-bot              last-backup mark, RU networks stamp
#   /var/backups/geoirb-vpn-bot          daily backups, 7 kept
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib.sh"

# The tunnel keys are created once, by `make deploy-exit`, and never here: a
# new file would give this side keys the exit server does not know, and all
# foreign traffic would stay dead until the next `make deploy-exit`.
TUNNEL="${TUNNEL_FILE:-$ROOT/secret/tunnel.yaml}"
if [[ ! -s "$TUNNEL" ]]; then
  echo "error: $TUNNEL is missing or empty. It holds the keys of the tunnel to the exit server: restore your copy. Only for a brand-new tunnel: run \`make deploy-exit\` first (it creates the file)." >&2
  exit 1
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -m 700 "$TMP/pkg"

echo "▶ Building linux/amd64 binary"
make -C "$ROOT" --no-print-directory build OUT="$TMP/pkg/bot"

echo "▶ Building the server config"
(umask 077 && "$ROOT/scripts/server-env.sh" >"$TMP/pkg/env")
cp "$ROOT"/deploy/*.sh "$ROOT"/deploy/*.service "$ROOT"/deploy/*.timer "$ROOT"/deploy/*.conf \
  "$ROOT"/deploy/*.nft "$ROOT/deploy/exit/geoirb-awg-exit.service" "$TMP/pkg/"
rm "$TMP/pkg"/*_test.sh

echo "▶ Rendering the exit tunnel conf"
EXIT_HOST="$(export ACCESS_FILE="$ROOT/secret/exit-access.yaml" && source "$ROOT/scripts/lib.sh" && echo "$SERVER_HOST")"
[[ -n "$EXIT_HOST" ]] || { echo "error: no host in secret/exit-access.yaml" >&2; exit 1; }
(umask 077 && "$ROOT/scripts/render-tunnel.sh" ru "$EXIT_HOST" >"$TMP/pkg/awg-exit.conf")

# Only a hash of BOT_TOKEN/DB_SECRET_KEY goes to the server (install.sh
# refuses to replace them unless FORCE=1).
LOCAL_HASH="$(grep -E '^(BOT_TOKEN|DB_SECRET_KEY)=' "$TMP/pkg/env" | sort | shasum -a 256 | cut -d' ' -f1)"

echo "▶ Installing on $SERVER_USER@$SERVER_HOST:$SERVER_PORT"
# One connection: the package goes through stdin (secrets never in argv),
# deploy/install.sh runs from it as root, then the status: exit 1 with the
# log when the bot is not up or restarted.
tar -C "$TMP/pkg" -cz . | remote "sudo LOCAL_HASH=$LOCAL_HASH FORCE=${FORCE:-} bash -euo pipefail -c '
S=\$(mktemp -d /tmp/geoirb-vpn-bot-deploy.XXXXXX)
tar -xz -C \$S
bash \$S/install.sh
echo \"▶ Status\"
sleep 8
for u in geoirb-awg0 geoirb-awg-exit geoirb-vpn-routes unbound; do echo \"\$u: \$(systemctl is-active \$u.service || true)\"; done
if systemctl is-active --quiet geoirb-vpn-bot.service && [ \"\$(systemctl show -p NRestarts --value geoirb-vpn-bot.service)\" = 0 ]; then
  systemctl --no-pager --lines=0 status geoirb-vpn-bot.service | head -5
  journalctl -u geoirb-vpn-bot.service -n 15 --no-pager -o cat
else
  echo \"error: the bot is not running (or restarted). Its log:\" >&2
  journalctl -u geoirb-vpn-bot.service -n 40 --no-pager -o cat >&2
  exit 1
fi
'"
echo "✔ Deployed"
