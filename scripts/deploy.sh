#!/usr/bin/env bash
# Builds the bot and installs (or updates) it on the VPN server as the
# systemd service geoirb-vpn-bot, plus the daily backup timer. Safe to
# re-run: the first run also creates the vpnbot user and the directories.
# Two ssh connections in all (install, then status): the server throttles
# many quick password logins.
#
# Server layout:
#   /opt/geoirb-vpn-bot/bot, backup.sh   binary and backup script
#   /etc/geoirb-vpn-bot/env              config (640 root:vpnbot), from scripts/server-env.sh
#   /var/lib/geoirb-vpn-bot              docker CLI home of vpnbot, last-backup mark
#   /var/backups/geoirb-vpn-bot          daily backups, 7 kept
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib.sh"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -m 700 "$TMP/pkg"

echo "▶ Building linux/amd64 binary"
make -C "$ROOT" --no-print-directory build OUT="$TMP/pkg/bot"

echo "▶ Building the server config"
(umask 077 && "$ROOT/scripts/server-env.sh" >"$TMP/pkg/env")
cp "$ROOT"/deploy/*.sh "$ROOT"/deploy/*.service "$ROOT"/deploy/*.timer "$ROOT"/deploy/*.conf "$TMP/pkg/"

# Only a hash of BOT_TOKEN/DB_SECRET_KEY goes to the server (install.sh
# refuses to replace them unless FORCE=1).
LOCAL_HASH="$(grep -E '^(BOT_TOKEN|DB_SECRET_KEY)=' "$TMP/pkg/env" | sort | shasum -a 256 | cut -d' ' -f1)"

echo "▶ Uploading and installing on $SERVER_USER@$SERVER_HOST:$SERVER_PORT"
# One connection: the package goes through stdin (secrets never in argv),
# then deploy/install.sh runs from it as root.
tar -C "$TMP/pkg" -cz . | remote "sudo LOCAL_HASH=$LOCAL_HASH FORCE=${FORCE:-} bash -euo pipefail -c '
S=\$(mktemp -d /tmp/geoirb-vpn-bot-deploy.XXXXXX)
tar -xz -C \$S
exec bash \$S/install.sh
'"

echo "▶ Status"
sleep 8
# One connection: exit 1 with the log when the bot is not up or restarted.
remote "sudo bash -c '
if systemctl is-active --quiet geoirb-vpn-bot.service && [ \"\$(systemctl show -p NRestarts --value geoirb-vpn-bot.service)\" = 0 ]; then
  systemctl --no-pager --lines=0 status geoirb-vpn-bot.service | head -5
  journalctl -u geoirb-vpn-bot.service -n 15 --no-pager -o cat
else
  echo \"error: the bot is not running (or restarted). Its log:\" >&2
  journalctl -u geoirb-vpn-bot.service -n 40 --no-pager -o cat >&2
  exit 1
fi'"
echo "✔ Deployed"
