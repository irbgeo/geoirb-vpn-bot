#!/usr/bin/env bash
# Runs as root on the RU VPN server from the unpacked deploy package (scripts/
# deploy.sh uploads it). Installs or updates the bot, the host AmneziaWG
# (awg0 for clients, awg-exit tunnel), split routing, DNS for clients, the
# backup timer and the host tuning. Order matters: a backup of the current
# state is taken first, before anything on the host changes.
# Env: LOCAL_HASH (hash of BOT_TOKEN/DB_SECRET_KEY in the package), FORCE=1
# to replace them anyway. ROOT is a path prefix for tests.
set -euo pipefail
S="$(cd "$(dirname "$0")" && pwd)" # removed by its creator (scripts/deploy.sh), not here
ROOT="${ROOT:-}"
E="$ROOT/etc/geoirb-vpn-bot/env"
OPT="$ROOT/opt/geoirb-vpn-bot"
SD="$ROOT/etc/systemd/system"

# The bot token and the key that encrypts client keys: a different one
# would take over another bot or make every stored key unreadable.
if [[ -f "$E" ]]; then
  server_hash="$(grep -E '^(BOT_TOKEN|DB_SECRET_KEY)=' "$E" | sort | sha256sum | cut -d' ' -f1)"
  if [[ "$server_hash" != "$LOCAL_HASH" && "${FORCE:-}" != 1 ]]; then
    echo "error: BOT_TOKEN or DB_SECRET_KEY in .env differs from the server. A new DB_SECRET_KEY makes every stored client key unreadable. If you really mean it: FORCE=1 make deploy" >&2
    exit 1
  fi
fi

# A backup of the state the running version left, before anything changes
# (only backup.sh and its unit are new by then, and it still reads the old
# env): a failed backup stops the deploy with the old bot, env, units and
# tunnels as they were. On the first deploy there is nothing to back up yet.
first=0
[[ -x "$OPT/bot" && -f "$E" ]] || first=1
if [[ "$first" == 0 ]]; then
  install -m 750 "$S/backup.sh" "$OPT/"
  install -m 644 "$S/geoirb-vpn-bot-backup.service" "$SD/"
  systemctl daemon-reload
  systemctl start geoirb-vpn-bot-backup.service
fi

# The module comes from ppa:amnezia/ppa (amneziawg-dkms), installed by hand.
modprobe amneziawg || { echo "error: kernel module amneziawg is missing" >&2; exit 1; }
if ! dpkg -s unbound nftables >/dev/null 2>&1; then
  apt-get update -qq
  apt-get install -y -qq unbound nftables
fi
bash "$S/awg-tools.sh"

id vpnbot >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin vpnbot
install -d -m 755 "$OPT"
install -m 750 "$S/backup.sh" "$S/ru-nets.sh" "$S/vpn-routes.sh" "$S/import-peers.sh" \
  "$S/awg0-check.sh" "$OPT/"
install -d -m 750 "$ROOT/etc/geoirb-vpn-bot"
chown root:vpnbot "$ROOT/etc/geoirb-vpn-bot"
install -m 640 "$S/env" "$E"
chown root:vpnbot "$E"

# Container-era leftovers (the Amnezia container's conntrack timer).
systemctl disable --quiet --now geoirb-vpn-conntrack.timer 2>/dev/null || true
rm -f "$SD"/geoirb-vpn-conntrack.* "$OPT/awg-conntrack.sh" "$OPT/awg-container.sh"

install -d "$SD/unbound.service.d" "$ROOT/etc/unbound/unbound.conf.d" "$ROOT/etc/sysctl.d" \
  "$ROOT/etc/modules-load.d" "$ROOT/etc/modprobe.d"
install -m 644 "$S"/*.service "$S"/*.timer "$SD/"
# unbound is restarted (below) only when one of its two files changed: a bot
# release must not cut the clients' DNS.
unbound_new=0
cmp -s "$S/unbound-after-awg0.conf" "$SD/unbound.service.d/geoirb.conf" || unbound_new=1
cmp -s "$S/unbound-geoirb.conf" "$ROOT/etc/unbound/unbound.conf.d/geoirb.conf" || unbound_new=1
install -m 644 "$S/unbound-after-awg0.conf" "$SD/unbound.service.d/geoirb.conf"
install -m 644 "$S/unbound-geoirb.conf" "$ROOT/etc/unbound/unbound.conf.d/geoirb.conf"
install -d -m 700 "$ROOT/etc/geoirb-vpn"
install -m 600 "$S/geoirb-vpn.nft" "$ROOT/etc/geoirb-vpn/"

# Host tuning (forwarding, conntrack size); no VPN restart needed.
install -m 644 "$S/99-geoirb-vpn.conf" "$ROOT/etc/sysctl.d/"
install -m 644 "$S/nf_conntrack-modules.conf" "$ROOT/etc/modules-load.d/nf_conntrack.conf"
install -m 644 "$S/amneziawg-modules.conf" "$ROOT/etc/modules-load.d/amneziawg.conf"
install -m 644 "$S/nf_conntrack-modprobe.conf" "$ROOT/etc/modprobe.d/nf_conntrack.conf"
modprobe nf_conntrack
# the live value follows the shipped file, so both always agree
sed -n 's/.*hashsize=//p' "$S/nf_conntrack-modprobe.conf" >"$ROOT/sys/module/nf_conntrack/parameters/hashsize"
sysctl -q -p "$ROOT/etc/sysctl.d/99-geoirb-vpn.conf"

# awg0.conf: created on the first install only, never overwritten.
ROOT="$ROOT" bash "$S/awg0-init.sh"
# The client subnet is written in more places than awg0.conf: geoirb-vpn.nft
# (mark, NAT), unbound-geoirb.conf, the ufw rule below and CLIENT_DNS
# (scripts/server-env.sh). They do not follow a hand-changed Address.
if ! grep -Eq '^Address[[:space:]]*=[[:space:]]*10\.8\.0\.1/22[[:space:]]*$' "$ROOT/etc/amnezia/amneziawg/awg0.conf"; then
  echo "warning: Address in awg0.conf is not 10.8.0.1/22. NAT, split routing and DNS are set for 10.8.0.0/22 only: clients outside it get no internet and no DNS. Change deploy/geoirb-vpn.nft, deploy/unbound-geoirb.conf, deploy/install.sh (ufw) and scripts/server-env.sh (CLIENT_DNS) to match." >&2
fi

# systemd-networkd drops foreign ip rules and routes whenever it reconfigures
# a link (see networkd-geoirb.conf). It reads this file at start only, so
# restart it once; a restart itself removes nothing, and the routes are
# (re)applied right after anyway.
ND="$ROOT/etc/systemd/networkd.conf.d"
if ! cmp -s "$S/networkd-geoirb.conf" "$ND/geoirb.conf"; then
  install -d "$ND"
  install -m 644 "$S/networkd-geoirb.conf" "$ND/geoirb.conf"
  if systemctl is-active --quiet systemd-networkd.service; then
    systemctl restart systemd-networkd.service
  fi
fi

systemctl daemon-reload
systemctl enable --quiet geoirb-awg0.service geoirb-awg-exit.service geoirb-vpn-routes.service
# Split routing first; it stays while the tunnel restarts. Reload, never
# restart: awg0 Requires= it, a restart would drop every client.
if systemctl is-active --quiet geoirb-vpn-routes.service; then
  systemctl reload geoirb-vpn-routes.service
else
  systemctl start geoirb-vpn-routes.service
fi
# and re-assert the rule and routes every minute, whatever drops them
systemctl enable --quiet --now geoirb-vpn-routes-check.timer
# The tunnel is restarted only when its conf changed: a bot release must not
# cut foreign traffic. Stop first: `down` must run with the OLD tunnel conf,
# before it is replaced.
if ! cmp -s "$S/awg-exit.conf" "$ROOT/etc/geoirb-vpn/awg-exit.conf"; then
  systemctl stop geoirb-awg-exit.service 2>/dev/null || true
  install -m 600 "$S/awg-exit.conf" "$ROOT/etc/geoirb-vpn/awg-exit.conf"
fi
# The tunnel first (`start` leaves an active unit alone): a failure here
# stops the deploy before anything else is started.
systemctl start geoirb-awg-exit.service
# start, never restart awg0: that would drop every connected client
systemctl is-active --quiet geoirb-awg0.service || systemctl start geoirb-awg0.service

# ufw (server-infra) drops forwarded and incoming traffic by default.
if command -v ufw >/dev/null && ufw status | grep '^Status: active' >/dev/null; then
  ufw route allow in on awg0
  ufw allow in on awg0 to 10.8.0.1 port 53
fi

# Ubuntu's unbound-resolvconf points the host resolver at 127.0.0.1, where
# this unbound does not listen.
systemctl disable --quiet --now unbound-resolvconf.service 2>/dev/null || true
systemctl enable --quiet unbound.service
if [[ "$unbound_new" == 1 ]]; then
  systemctl restart unbound.service
else
  systemctl start unbound.service
fi
systemctl enable --quiet --now geoirb-vpn-mss.service
systemctl enable --quiet --now geoirb-ru-nets.timer
# fill the RU set now; a failed download must not stop the deploy (it retries)
systemctl start geoirb-ru-nets.service || echo "warning: RU networks not loaded yet, see journalctl -u geoirb-ru-nets" >&2
systemctl enable --quiet --now geoirb-vpn-bot-backup.timer

install -m 755 "$S/bot" "$OPT/bot"
systemctl enable --quiet geoirb-vpn-bot.service
systemctl restart geoirb-vpn-bot.service
# the first deploy: a backup now, so the bot has a last-backup mark
[[ "$first" == 0 ]] || systemctl start geoirb-vpn-bot-backup.service
