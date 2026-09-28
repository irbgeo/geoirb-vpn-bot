#!/usr/bin/env bash
# Runs as root on the VPN server from the unpacked deploy package (scripts/
# deploy.sh uploads it). Installs or updates the bot, its backup timer and
# the host tuning. Order matters: a backup of the current state is taken
# before the new binary can touch Mongo or the Amnezia config.
# Env: LOCAL_HASH (hash of BOT_TOKEN/DB_SECRET_KEY in the package), FORCE=1
# to replace them anyway.
set -euo pipefail
S="$(cd "$(dirname "$0")" && pwd)"
trap 'rm -rf "$S"' EXIT
E=/etc/geoirb-vpn-bot/env
OPT=/opt/geoirb-vpn-bot

# The bot token and the key that encrypts client keys: a different one
# would take over another bot or make every stored key unreadable.
if [[ -f "$E" ]]; then
  server_hash="$(grep -E '^(BOT_TOKEN|DB_SECRET_KEY)=' "$E" | sort | sha256sum | cut -d' ' -f1)"
  if [[ "$server_hash" != "$LOCAL_HASH" && "${FORCE:-}" != 1 ]]; then
    echo "error: BOT_TOKEN or DB_SECRET_KEY in .env differs from the server. A new DB_SECRET_KEY makes every stored client key unreadable. If you really mean it: FORCE=1 make deploy" >&2
    exit 1
  fi
fi

id vpnbot >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin vpnbot
usermod -aG docker vpnbot
install -d -m 755 "$OPT"
install -m 750 "$S/backup.sh" "$S/awg-conntrack.sh" "$OPT/"
install -m 640 "$S/awg-container.sh" "$OPT/"
install -d -m 750 -o root -g vpnbot /etc/geoirb-vpn-bot
install -m 640 -o root -g vpnbot "$S/env" "$E"
install -m 644 "$S"/*.service "$S"/*.timer /etc/systemd/system/

# VPN host tuning (conntrack size, TCP MSS clamp); no VPN restart needed.
install -m 644 "$S/99-geoirb-vpn.conf" /etc/sysctl.d/
install -m 644 "$S/nf_conntrack-modules.conf" /etc/modules-load.d/nf_conntrack.conf
install -m 644 "$S/amneziawg-modules.conf" /etc/modules-load.d/amneziawg.conf
install -m 644 "$S/nf_conntrack-modprobe.conf" /etc/modprobe.d/nf_conntrack.conf
modprobe nf_conntrack
# the live value follows the shipped file, so both always agree
sed -n 's/.*hashsize=//p' "$S/nf_conntrack-modprobe.conf" >/sys/module/nf_conntrack/parameters/hashsize
sysctl -q -p /etc/sysctl.d/99-geoirb-vpn.conf

systemctl daemon-reload
systemctl enable --quiet --now geoirb-vpn-mss.service
systemctl enable --quiet --now geoirb-vpn-conntrack.timer
# a missing container must not stop the deploy; the timer tries again
systemctl start geoirb-vpn-conntrack.service || echo "warning: conntrack timeout not copied into the Amnezia container yet" >&2
systemctl enable --quiet --now geoirb-vpn-bot-backup.timer

# A backup of the state the running version left, before the new one
# starts; a failed backup stops the deploy with the old bot still running.
# On the first deploy there is nothing to back up yet.
first=0
[[ -x "$OPT/bot" ]] || first=1
[[ "$first" == 1 ]] || systemctl start geoirb-vpn-bot-backup.service

install -m 755 "$S/bot" "$OPT/bot"
systemctl enable --quiet geoirb-vpn-bot.service
systemctl restart geoirb-vpn-bot.service
# the first deploy: a backup now, so the bot has a last-backup mark
[[ "$first" == 0 ]] || systemctl start geoirb-vpn-bot-backup.service
