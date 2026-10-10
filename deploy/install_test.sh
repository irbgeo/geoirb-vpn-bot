#!/usr/bin/env bash
# Tests deploy/install.sh (RU server) with ROOT=<tmp> and fake commands on PATH.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$DIR/../scripts/testlib.sh"
has() { [[ -f "$1" ]] && echo 1 || echo 0; }
line() { grep -n "$1" "$TMP/calls" | head -1 | cut -d: -f1; } # first call matching

TAG="$(sed -n 's/^AWG_TOOLS_TAG="${AWG_TOOLS_TAG-\([^}]*\)}".*/\1/p' "$DIR/awg-tools.sh")"
R="$TMP/root"
SD="$R/etc/systemd/system"
OPT="$R/opt/geoirb-vpn-bot"

mkdir "$TMP/bin"
for c in modprobe sysctl nft ip useradd usermod apt-get chown git make iptables; do
  printf '#!/usr/bin/env bash\necho "%s $*" >>"$CALLS"\n' "$c" >"$TMP/bin/$c"
done
# dpkg -s: packages missing unless $DPKG_OK is set.
printf '#!/usr/bin/env bash\n[[ -n "${DPKG_OK:-}" ]]\n' >"$TMP/bin/dpkg"
# id: vpnbot is missing, so useradd runs.
printf '#!/usr/bin/env bash\nexit 1\n' >"$TMP/bin/id"
# awg: --version prints the pinned tag (no build); genkey for awg0-init.
printf '#!/usr/bin/env bash\necho "awg $*" >>"$CALLS"\n[[ "$1" == genkey ]] && echo KEY || echo "amneziawg-tools %s"\n' "$TAG" >"$TMP/bin/awg"
printf '#!/usr/bin/env bash\necho "ufw $*" >>"$CALLS"\n[[ "$1" != status ]] || echo "Status: $UFW"\n' >"$TMP/bin/ufw"
# systemctl also logs the awg-exit conf and the bot binary it sees, to prove the order.
printf '#!/usr/bin/env bash\necho "systemctl $* conf=$(cat "$ROOT/etc/geoirb-vpn/awg-exit.conf" 2>/dev/null) bot=$(cat "$ROOT/opt/geoirb-vpn-bot/bot" 2>/dev/null)" >>"$CALLS"\ncase "$*" in\n  *is-active*geoirb-vpn-routes*) [[ -n "${ROUTES_ACTIVE:-}" ]] ;;\n  *is-active*geoirb-awg0*) [[ -n "${AWG0_ACTIVE:-}" ]] ;;\n  *is-active*systemd-networkd*) [[ -z "${NO_NETWORKD:-}" ]] ;;\n  *start*geoirb-vpn-bot-backup*) [[ -z "${BACKUP_FAIL:-}" ]] ;;\nesac\n' >"$TMP/bin/systemctl"
chmod +x "$TMP/bin"/*

run() { # run <ufw state> [env...]: installs a fresh copy of the package as deploy.sh lays it out.
  rm -rf "$TMP/pkg"; mkdir "$TMP/pkg"
  cp "$DIR"/*.sh "$DIR"/*.service "$DIR"/*.timer "$DIR"/*.conf "$DIR"/*.nft "$DIR/exit/geoirb-awg-exit.service" "$TMP/pkg/"
  echo "[Interface] NEW" >"$TMP/pkg/awg-exit.conf"
  echo NEWBOT >"$TMP/pkg/bot"
  printf 'BOT_TOKEN=1:a\nDB_SECRET_KEY=k\n' >"$TMP/pkg/env"
  : >"$TMP/calls"
  local ufw="$1"; shift
  env CALLS="$TMP/calls" UFW="$ufw" ROOT="$R" PATH="$TMP/bin:$PATH" LOCAL_HASH="$HASH" "$@" \
    bash "$TMP/pkg/install.sh" >"$TMP/out" 2>&1
  local rc=$?
  cat "$TMP/calls" >>"$TMP/all-calls"
  return $rc
}
HASH="$(printf 'BOT_TOKEN=1:a\nDB_SECRET_KEY=k\n' | sort | sha256sum | cut -d' ' -f1)"

# First install; leftovers of the container era must go.
mkdir -p "$R/sys/module/nf_conntrack/parameters" "$SD" "$OPT" "$R/etc/geoirb-vpn"
touch "$SD/geoirb-vpn-conntrack.service" "$SD/geoirb-vpn-conntrack.timer" "$OPT/awg-conntrack.sh" "$OPT/awg-container.sh"
echo "[Interface] OLD" >"$R/etc/geoirb-vpn/awg-exit.conf"
run active
check "first install exits 0" "0" "$?"
check "install.sh does not delete its own directory" "1" "$(has "$TMP/pkg/install.sh")"
for u in geoirb-vpn-bot.service geoirb-awg0.service geoirb-awg-exit.service geoirb-vpn-routes.service \
  geoirb-ru-nets.service geoirb-ru-nets.timer geoirb-vpn-mss.service geoirb-vpn-bot-backup.service geoirb-vpn-bot-backup.timer \
  geoirb-vpn-routes-check.service geoirb-vpn-routes-check.timer; do
  check "unit $u installed" "1" "$(has "$SD/$u")"
done
# systemd-networkd must keep the ip rules and routes it did not create.
ND="$R/etc/systemd/networkd.conf.d/geoirb.conf"
check "networkd keeps foreign rules and routes" "1 1" \
  "$(grep -cx 'ManageForeignRoutingPolicyRules=no' "$ND") $(grep -cx 'ManageForeignRoutes=no' "$ND")"
check "networkd restarted once to read it" "1" "$(grep -c '^systemctl restart systemd-networkd' "$TMP/calls")"
check "networkd restarted before the routes are applied" "1" "$([[ "$(line '^systemctl restart systemd-networkd')" -lt "$(line '^systemctl start geoirb-vpn-routes')" ]] && echo 1)"
check "routes check timer on" "1" "$(grep -c '^systemctl enable --quiet --now geoirb-vpn-routes-check.timer' "$TMP/calls")"
check "routes check runs the rules mode" "ExecStart=/opt/geoirb-vpn-bot/vpn-routes.sh rules" "$(grep '^ExecStart' "$SD/geoirb-vpn-routes-check.service")"
check "unbound drop-in" "1" "$(has "$SD/unbound.service.d/geoirb.conf")"
check "unbound conf" "1" "$(has "$R/etc/unbound/unbound.conf.d/geoirb.conf")"
check "nft file" "1" "$(has "$R/etc/geoirb-vpn/geoirb-vpn.nft")"
check "scripts in /opt" "1 1 1" "$(has "$OPT/backup.sh") $(has "$OPT/ru-nets.sh") $(has "$OPT/awg0-check.sh")"
check "conntrack timer files gone" "0 0 0 0" \
  "$(has "$SD/geoirb-vpn-conntrack.service") $(has "$SD/geoirb-vpn-conntrack.timer") $(has "$OPT/awg-conntrack.sh") $(has "$OPT/awg-container.sh")"
check "awg0.conf created" "1" "$(grep -c '^Address = 10.8.0.1/22' "$R/etc/amnezia/amneziawg/awg0.conf")"
check "awg-exit.conf replaced" "[Interface] NEW" "$(cat "$R/etc/geoirb-vpn/awg-exit.conf")"
check "awg-exit.conf mode" "600" "$(mode "$R/etc/geoirb-vpn/awg-exit.conf")"
check "awg-exit stopped with the old conf" "1" "$(grep -c '^systemctl stop geoirb-awg-exit.service conf=\[Interface\] OLD' "$TMP/calls")"
check "awg-exit started after the stop" "1" "$([[ "$(line '^systemctl stop geoirb-awg-exit')" -lt "$(line '^systemctl start geoirb-awg-exit')" ]] && echo 1)"
check "inactive awg0 started" "1" "$(grep -c '^systemctl start geoirb-awg0' "$TMP/calls")"
check "the tunnel is started before awg0" "1" "$([[ "$(line '^systemctl start geoirb-awg-exit')" -lt "$(line '^systemctl start geoirb-awg0')" ]] && echo 1)"
check "inactive routes started" "1 0" "$(grep -c '^systemctl start geoirb-vpn-routes' "$TMP/calls") $(grep -c '^systemctl reload geoirb-vpn-routes' "$TMP/calls")"
check "routes not stopped" "0" "$(grep -c '^systemctl stop geoirb-vpn-routes' "$TMP/calls")"
check "routes before the tunnels" "1" "$([[ "$(line '^systemctl start geoirb-vpn-routes')" -lt "$(line '^systemctl start geoirb-awg0')" ]] && echo 1)"
check "routes script in /opt" "1" "$(has "$OPT/vpn-routes.sh")"
U="$SD/geoirb-vpn-routes.service"
check "routes unit: no PartOf, no ExecStop" "0" "$(grep -cE '^(PartOf|ExecStop)' "$U")"
check "routes unit: Before both tunnels" "1" "$(grep -cx 'Before=geoirb-awg0.service geoirb-awg-exit.service' "$U")"
check "routes unit: wanted by multi-user only" "WantedBy=multi-user.target" "$(grep '^WantedBy' "$U")"
check "routes unit: start and reload run the same script" "ExecStart=/opt/geoirb-vpn-bot/vpn-routes.sh
ExecReload=/opt/geoirb-vpn-bot/vpn-routes.sh" "$(grep -E '^Exec' "$U")"
check "awg0 unit requires the routes unit" "1" "$(grep -cx 'Requires=geoirb-vpn-routes.service' "$SD/geoirb-awg0.service")"
for u in geoirb-awg0 geoirb-awg-exit; do
  check "$u: a failed start is retried" "Restart=on-failure RestartSec=10" "$(grep -E '^Restart(Sec)?=' "$SD/$u.service" | tr '\n' ' ' | sed 's/ $//')"
done
check "unbound-resolvconf off" "1" "$(grep -c '^systemctl disable --quiet --now unbound-resolvconf.service' "$TMP/calls")"
check "apt-get update before install" "1 1" "$(grep -c '^apt-get update' "$TMP/calls") $([[ "$(line '^apt-get update')" -lt "$(line '^apt-get install')" ]] && echo 1)"
check "units enabled" "1" "$(grep -c '^systemctl enable.*geoirb-awg0.service geoirb-awg-exit.service geoirb-vpn-routes.service' "$TMP/calls")"
check "unbound restarted" "1" "$(grep -c '^systemctl restart unbound' "$TMP/calls")"
check "ru-nets timer on" "1" "$(grep -c '^systemctl enable --quiet --now geoirb-ru-nets.timer' "$TMP/calls")"
check "packages" "1" "$(grep -c '^apt-get install.* unbound nftables' "$TMP/calls")"
check "module checked" "1" "$(grep -c '^modprobe amneziawg' "$TMP/calls")"
check "vpnbot created" "1" "$(grep -c '^useradd .*vpnbot' "$TMP/calls")"
check "no docker group" "0" "$(grep -c 'usermod' "$TMP/calls")"
check "ufw active: route allowed" "1" "$(grep -c '^ufw route allow in on awg0$' "$TMP/calls")"
check "ufw active: DNS allowed" "1" "$(grep -c '^ufw allow in on awg0 to 10.8.0.1 port 53$' "$TMP/calls")"
check "first install: backup after the bot start" "1" "$([[ "$(line '^systemctl restart geoirb-vpn-bot.service')" -lt "$(line '^systemctl start geoirb-vpn-bot-backup.service')" ]] && echo 1)"
check "bot installed" "NEWBOT" "$(cat "$OPT/bot")"

# Update: the backup sees the old binary; ufw inactive adds no rules.
echo OLDBOT >"$OPT/bot"
cp "$R/etc/amnezia/amneziawg/awg0.conf" "$TMP/awg0.first"
run inactive DPKG_OK=1 ROUTES_ACTIVE=1 AWG0_ACTIVE=1
check "update exits 0" "0" "$?"
check "update: active routes reloaded, not started" "1 0" "$(grep -c '^systemctl reload geoirb-vpn-routes' "$TMP/calls") $(grep -c '^systemctl start geoirb-vpn-routes' "$TMP/calls")"
check "update: active awg0 left alone" "0" "$(grep -c '^systemctl start geoirb-awg0' "$TMP/calls")"
# A bot release must not cut foreign traffic or DNS: nothing of theirs changed.
check "update: same tunnel conf, tunnel not stopped, only made sure it runs" "0 1" \
  "$(grep -c '^systemctl stop geoirb-awg-exit' "$TMP/calls") $(grep -c '^systemctl start geoirb-awg-exit' "$TMP/calls")"
check "update: same unbound files, unbound not restarted, only made sure it runs" "0 1" \
  "$(grep -c '^systemctl restart unbound' "$TMP/calls") $(grep -c '^systemctl start unbound' "$TMP/calls")"
check "update: networkd conf unchanged, not restarted" "0" "$(grep -c '^systemctl restart systemd-networkd' "$TMP/calls")"
check "update: backup of the old state" "1" "$(grep -c '^systemctl start geoirb-vpn-bot-backup.service .*bot=OLDBOT' "$TMP/calls")"
check "update: one backup" "1" "$(grep -c '^systemctl start geoirb-vpn-bot-backup.service' "$TMP/calls")"
check "update: the backup comes before any other change" "2" "$(line '^systemctl start geoirb-vpn-bot-backup.service')"
check "update: awg0.conf kept" "1" "$(cmp -s "$R/etc/amnezia/amneziawg/awg0.conf" "$TMP/awg0.first" && echo 1)"
check "ufw inactive: no rules" "0" "$(grep -c '^ufw .*allow' "$TMP/calls")"
check "packages present: no apt-get" "0" "$(grep -c '^apt-get' "$TMP/calls")"
check "Address 10.8.0.1/22: no subnet warning" "0" "$(grep -c 'warning: Address in awg0.conf' "$TMP/out")"
check "conf without hooks: no hook warning" "0" "$(grep -c 'warning: awg0 will NOT start' "$TMP/out")"

# A hook line in the live conf: the awg0 unit would refuse it at its next start; the deploy says so and goes on.
{ cat "$TMP/awg0.first"; echo "PostUp = id"; } >"$R/etc/amnezia/amneziawg/awg0.conf"
run inactive DPKG_OK=1 ROUTES_ACTIVE=1 AWG0_ACTIVE=1
check "hook line in awg0.conf: warned, the deploy goes on" "0 1" "$? $(grep -c 'warning: awg0 will NOT start again' "$TMP/out")"
cp "$TMP/awg0.first" "$R/etc/amnezia/amneziawg/awg0.conf"

# NAT, DNS and split routing are written for 10.8.0.0/22: a hand-widened Address is reported.
sed 's|^Address = .*|Address = 10.8.0.1/21|' "$TMP/awg0.first" >"$R/etc/amnezia/amneziawg/awg0.conf"
run inactive DPKG_OK=1 ROUTES_ACTIVE=1 AWG0_ACTIVE=1
check "another Address: warned, the deploy goes on" "0 1" "$? $(grep -c 'warning: Address in awg0.conf is not 10.8.0.1/22' "$TMP/out")"
cp "$TMP/awg0.first" "$R/etc/amnezia/amneziawg/awg0.conf"

# A failed backup stops the deploy with everything as the old version left it.
echo OLDBOT >"$OPT/bot"
printf 'BOT_TOKEN=1:a\nDB_SECRET_KEY=k\nOLD_ENV=1\n' >"$R/etc/geoirb-vpn-bot/env"
echo "# old unit" >"$SD/geoirb-vpn-bot.service"
run inactive DPKG_OK=1 ROUTES_ACTIVE=1 AWG0_ACTIVE=1 BACKUP_FAIL=1
check "failed backup: the deploy fails" "1" "$([[ $? -ne 0 ]] && echo 1)"
check "failed backup: old binary, env and bot unit kept" "OLDBOT OLD_ENV=1 # old unit" \
  "$(cat "$OPT/bot") $(tail -1 "$R/etc/geoirb-vpn-bot/env") $(cat "$SD/geoirb-vpn-bot.service")"
check "failed backup: nothing after it ran (no bot restart, no tunnel stop)" "systemctl daemon-reload
systemctl start geoirb-vpn-bot-backup.service" "$(sed 's/ conf=.*//' "$TMP/calls")"
rm "$R/etc/geoirb-vpn-bot/env" # as on a host with a binary but no env: no early backup, the deploy goes on
run inactive DPKG_OK=1 ROUTES_ACTIVE=1 AWG0_ACTIVE=1
check "after the failed one: a deploy passes" "0 NEWBOT" "$? $(cat "$OPT/bot")"

# A changed unbound file or tunnel conf does restart its service.
echo "# old" >>"$R/etc/unbound/unbound.conf.d/geoirb.conf"
echo "[Interface] OLD" >"$R/etc/geoirb-vpn/awg-exit.conf"
run inactive DPKG_OK=1 ROUTES_ACTIVE=1 AWG0_ACTIVE=1
check "changed unbound conf: restarted" "1" "$(grep -c '^systemctl restart unbound' "$TMP/calls")"
check "changed tunnel conf: stopped with the old conf, then started" "1 1" \
  "$(grep -c '^systemctl stop geoirb-awg-exit.service conf=\[Interface\] OLD' "$TMP/calls") $([[ "$(line '^systemctl stop geoirb-awg-exit')" -lt "$(line '^systemctl start geoirb-awg-exit')" ]] && echo 1)"

# A host that does not run systemd-networkd: the file is installed, nothing is restarted.
rm -f "$R/etc/systemd/networkd.conf.d/geoirb.conf"
run inactive DPKG_OK=1 ROUTES_ACTIVE=1 AWG0_ACTIVE=1 NO_NETWORKD=1
check "no networkd: conf installed, no restart" "1 0" "$(has "$R/etc/systemd/networkd.conf.d/geoirb.conf") $(grep -c '^systemctl restart systemd-networkd' "$TMP/calls")"

# The env on the server has another BOT_TOKEN/DB_SECRET_KEY.
printf 'BOT_TOKEN=2:b\nDB_SECRET_KEY=other\n' >"$R/etc/geoirb-vpn-bot/env"
run active
check "hash guard fails" "1" "$?"
check "hash guard: nothing done" "0" "$(grep -c . "$TMP/calls")"
check "hash guard: env kept" "BOT_TOKEN=2:b" "$(head -1 "$R/etc/geoirb-vpn-bot/env")"
run active FORCE=1
check "FORCE=1 replaces" "0" "$?"

check "never restarts routes or awg0" "0" "$(cat "$TMP"/all-calls | grep -c '^systemctl restart geoirb-\(vpn-routes\|awg0\)')"

finish
