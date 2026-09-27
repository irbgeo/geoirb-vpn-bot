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
(cd "$ROOT" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$TMP/pkg/bot" ./cmd/bot)

echo "▶ Building the server config"
(umask 077 && "$ROOT/scripts/server-env.sh" >"$TMP/pkg/env")
cp "$ROOT"/deploy/backup.sh "$ROOT"/deploy/geoirb-vpn-bot.service \
  "$ROOT"/deploy/geoirb-vpn-bot-backup.service "$ROOT"/deploy/geoirb-vpn-bot-backup.timer \
  "$ROOT"/deploy/99-geoirb-vpn.conf "$ROOT"/deploy/nf_conntrack-*.conf "$ROOT"/deploy/geoirb-vpn-mss.service "$TMP/pkg/"

# The bot token and the key that encrypts client keys come from the local
# .env. A different one would take over another bot or make every stored
# key unreadable, so the server refuses a change unless FORCE=1. Only
# hashes are compared.
LOCAL_HASH="$(grep -E '^(BOT_TOKEN|DB_SECRET_KEY)=' "$TMP/pkg/env" | sort | shasum -a 256 | cut -d' ' -f1)"

echo "▶ Uploading and installing on $SERVER_USER@$SERVER_HOST:$SERVER_PORT"
# One connection: the package goes through stdin (secrets never in argv).
tar -C "$TMP/pkg" -cz . | remote "sudo LOCAL_HASH=$LOCAL_HASH FORCE=${FORCE:-} bash -euo pipefail -c '
S=/tmp/geoirb-vpn-bot-deploy
rm -rf \$S && mkdir -m 700 \$S && tar -xz -C \$S
E=/etc/geoirb-vpn-bot/env
if [ -f \$E ]; then
  server_hash=\$(grep -E \"^(BOT_TOKEN|DB_SECRET_KEY)=\" \$E | sort | sha256sum | cut -d\" \" -f1)
  if [ \"\$server_hash\" != \"\$LOCAL_HASH\" ] && [ \"\$FORCE\" != 1 ]; then
    rm -rf \$S
    echo \"error: BOT_TOKEN or DB_SECRET_KEY in .env differs from the server. A new DB_SECRET_KEY makes every stored client key unreadable. If you really mean it: FORCE=1 make deploy\" >&2
    exit 1
  fi
fi
id vpnbot >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin vpnbot
usermod -aG docker vpnbot
install -d -m 755 /opt/geoirb-vpn-bot
install -m 755 \$S/bot /opt/geoirb-vpn-bot/bot
install -m 750 \$S/backup.sh /opt/geoirb-vpn-bot/backup.sh
install -d -m 750 -o root -g vpnbot /etc/geoirb-vpn-bot
install -m 640 -o root -g vpnbot \$S/env \$E
install -m 644 \$S/geoirb-vpn-bot.service \$S/geoirb-vpn-bot-backup.service \$S/geoirb-vpn-bot-backup.timer /etc/systemd/system/
# VPN host tuning (conntrack size, TCP MSS clamp); no VPN restart needed
install -m 644 \$S/99-geoirb-vpn.conf /etc/sysctl.d/
install -m 644 \$S/nf_conntrack-modules.conf /etc/modules-load.d/nf_conntrack.conf
install -m 644 \$S/nf_conntrack-modprobe.conf /etc/modprobe.d/nf_conntrack.conf
install -m 644 \$S/geoirb-vpn-mss.service /etc/systemd/system/
modprobe nf_conntrack
echo 16384 >/sys/module/nf_conntrack/parameters/hashsize
sysctl -q -p /etc/sysctl.d/99-geoirb-vpn.conf
rm -rf \$S
systemctl daemon-reload
systemctl enable --quiet --now geoirb-vpn-mss.service
systemctl enable --quiet geoirb-vpn-bot.service
systemctl enable --quiet --now geoirb-vpn-bot-backup.timer
systemctl restart geoirb-vpn-bot.service
# a backup now, so the bot has a fresh last-backup mark from the start
systemctl start geoirb-vpn-bot-backup.service
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
