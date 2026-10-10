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
cat >"$TMP/bin/systemctl" <<'X'
#!/usr/bin/env bash
echo "systemctl $* conf=$(cat "$ROOT/etc/geoirb-vpn/awg-exit.conf" 2>/dev/null) bot=$(cat "$ROOT/opt/geoirb-vpn-bot/bot" 2>/dev/null)" >>"$CALLS"
case "$*" in
  *is-active*geoirb-vpn-routes*) [[ -n "${ROUTES_ACTIVE:-}" ]] ;;
  *is-active*geoirb-awg0*) [[ -n "${AWG0_ACTIVE:-}" ]] ;;
  *is-active*systemd-networkd*) [[ -z "${NO_NETWORKD:-}" ]] ;;
  # like backup.sh: the stamp path comes from the env installed at that moment
  *start*geoirb-vpn-bot-backup*)
    [[ -z "${BACKUP_FAIL:-}" ]] || exit 1
    s="$(sed -n 's/^BACKUP_STAMP=//p' "$ROOT/etc/geoirb-vpn-bot/env" 2>/dev/null)"
    [[ -z "$s" ]] || { mkdir -p "$ROOT$(dirname "$s")" && touch "$ROOT$s"; } ;;
  # like the new ru-nets.sh: the set and the stamp go to the root-owned directory
  *start*geoirb-ru-nets.service*)
    [[ -z "${RU_NETS_FAIL:-}" ]] || exit 1
    printf 'flush set inet geoirb ru4\nadd element inet geoirb ru4 {\n77.88.0.0/18\n}\n' >"$ROOT/var/lib/geoirb-vpn/ru4.nft"
    touch "$ROOT/var/lib/geoirb-vpn/ru-nets.stamp" ;;
  # what the stopping old bot and the starting new one can see
  *restart*geoirb-vpn-bot.service*)
    { ls "$ROOT/var/lib/geoirb-vpn" | sed 's/^/new:/'; ls "$ROOT/var/lib/geoirb-vpn-bot" 2>/dev/null | sed 's/^/old:/'; } >"$CALLS.state" ;;
esac
X
chmod +x "$TMP/bin"/*

run() { # run <ufw state> [env...]: installs a fresh copy of the package as deploy.sh lays it out.
  rm -rf "$TMP/pkg"; mkdir "$TMP/pkg"
  cp "$DIR"/*.sh "$DIR"/*.service "$DIR"/*.timer "$DIR"/*.conf "$DIR"/*.nft "$DIR/exit/geoirb-awg-exit.service" "$TMP/pkg/"
  echo "[Interface] NEW" >"$TMP/pkg/awg-exit.conf"
  echo NEWBOT >"$TMP/pkg/bot"
  printf 'BOT_TOKEN=1:a\nDB_SECRET_KEY=k\nBACKUP_STAMP=/var/lib/geoirb-vpn/last-backup\n' >"$TMP/pkg/env"
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
# A clean server: the root-owned state directory is made, nothing to migrate.
NEW="$R/var/lib/geoirb-vpn"
OLD="$R/var/lib/geoirb-vpn-bot"
check "first install: root state dir 755, made before ru-nets writes to it" "755 1" \
  "$(mode "$NEW") $([[ "$(line "^chown root:root $NEW")" -lt "$(line '^systemctl start geoirb-ru-nets.service')" ]] && echo 1)"
check "first install: set and both stamps there, no old directory, no warning" "last-backup ru-nets.stamp ru4.nft 0 0" \
  "$(ls "$NEW" | tr '\n' ' ')$([[ -e "$OLD" ]] && echo 1 || echo 0) $(grep -c 'warning' "$TMP/out")"

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

# Migration from the bot-owned state directory (audit #25).
GOOD_SET='flush set inet geoirb ru4
add element inet geoirb ru4 {
5.8.0.0/19,
31.13.0.0/24
}'
touch -t 200101010000 "$TMP/y2001"
old_layout() { # old_layout <ru4.nft content>: a server as the previous version left it
  rm -rf "$NEW" "$OLD"; mkdir -p "$OLD"
  printf 'BOT_TOKEN=1:a\nDB_SECRET_KEY=k\nBACKUP_STAMP=/var/lib/geoirb-vpn-bot/last-backup\n' >"$R/etc/geoirb-vpn-bot/env"
  touch -t 200001010000 "$OLD/last-backup" "$OLD/ru-nets.stamp"
  touch "$OLD/maintenance"
  printf '%s\n' "$1" >"$OLD/ru4.nft"
}

old_layout "$GOOD_SET"
run inactive DPKG_OK=1 ROUTES_ACTIVE=1 AWG0_ACTIVE=1
check "migration: exits 0, no warning" "0 0" "$? $(grep -c 'warning' "$TMP/out")"
check "migration: the three files are in the root directory, mode 644" "last-backup ru-nets.stamp ru4.nft 644 644 644" \
  "$(ls "$NEW" | tr '\n' ' ')$(mode "$NEW/last-backup") $(mode "$NEW/ru-nets.stamp") $(mode "$NEW/ru4.nft")"
# The early backup ran with the old env and wrote the OLD stamp: its fresh time is carried over.
check "migration: the backup stamp is fresh" "1" "$([[ "$NEW/last-backup" -nt "$TMP/y2001" ]] && echo 1)"
check "migration: a good download wins over the old set" "1" "$(grep -c '^77.88.0.0/18$' "$NEW/ru4.nft")"
check "migration: old files gone, the maintenance flag and the directory stay" "maintenance" "$(ls "$OLD")"
check "migration: at the bot restart both layouts are complete" "new:last-backup new:ru-nets.stamp new:ru4.nft old:last-backup old:maintenance old:ru-nets.stamp old:ru4.nft " \
  "$(tr '\n' ' ' <"$TMP/calls.state")"
check "migration: routes reloaded, never restarted" "1 0" "$(grep -c '^systemctl reload geoirb-vpn-routes' "$TMP/calls") $(grep -c '^systemctl restart geoirb-vpn-routes' "$TMP/calls")"

# Already migrated, and this time the download fails: nothing is touched.
touch -t 200001010000 "$NEW/ru-nets.stamp"
cp -p "$NEW/ru4.nft" "$TMP/ru4.migrated"
run inactive DPKG_OK=1 ROUTES_ACTIVE=1 AWG0_ACTIVE=1 RU_NETS_FAIL=1
check "second deploy: exits 0, set and stamp as they were, nothing new" "0 1 1 last-backup ru-nets.stamp ru4.nft maintenance" \
  "$? $(cmp -s "$NEW/ru4.nft" "$TMP/ru4.migrated" && echo 1) $([[ "$NEW/ru-nets.stamp" -ot "$TMP/y2001" ]] && echo 1) $(ls "$NEW" | tr '\n' ' ')$(ls "$OLD")"
check "second deploy: only the download warning" "1 0" "$(grep -c 'warning: RU networks not loaded yet' "$TMP/out") $(grep -c 'not copied' "$TMP/out")"

# The download fails on the migrating deploy: the old set is taken after the format check.
old_layout "$GOOD_SET"
run inactive DPKG_OK=1 ROUTES_ACTIVE=1 AWG0_ACTIVE=1 RU_NETS_FAIL=1
check "failed download, good old set: copied, 644, no temp file" "0 $GOOD_SET 644 last-backup ru-nets.stamp ru4.nft " \
  "$? $(cat "$NEW/ru4.nft") $(mode "$NEW/ru4.nft") $(ls "$NEW" | tr '\n' ' ')"
check "failed download: the RU stamp keeps its old time, the backup stamp is fresh" "1 1" \
  "$([[ "$NEW/ru-nets.stamp" -ot "$TMP/y2001" ]] && echo 1) $([[ "$NEW/last-backup" -nt "$TMP/y2001" ]] && echo 1)"
check "failed download, good old set: old files gone, no copy warning" "maintenance 0" "$(ls "$OLD") $(grep -c 'not copied' "$TMP/out")"

# Content from the bot-owned directory is not trusted: anything but the exact format is left behind.
n=0
for bad in "$GOOD_SET
add rule inet geoirb pre accept" 'include "/etc/shadow"' "flush ruleset" "add element inet geoirb ru4 { 0.0.0.0/0 }" \
  "5.8.0.0/19; flush ruleset" " 5.8.0.0/19" "$GOOD_SET
" ""; do
  n=$((n + 1))
  old_layout "$bad"
  run inactive DPKG_OK=1 ROUTES_ACTIVE=1 AWG0_ACTIVE=1 RU_NETS_FAIL=1
  check "bad old set $n: not copied, warned, deploy goes on" "0 last-backup ru-nets.stamp 1 maintenance" \
    "$? $(ls "$NEW" | tr '\n' ' ')$(grep -c 'warning: .*not copied' "$TMP/out") $(ls "$OLD")"
done
# A symlink planted as the old set (here to a well-formed file) is not followed.
old_layout x
printf '%s\n' "$GOOD_SET" >"$TMP/elsewhere"
ln -sf "$TMP/elsewhere" "$OLD/ru4.nft"
run inactive DPKG_OK=1 ROUTES_ACTIVE=1 AWG0_ACTIVE=1 RU_NETS_FAIL=1
check "symlink as the old set: not copied, removed, its target kept" "0 0 maintenance 1" \
  "$? $(has "$NEW/ru4.nft") $(ls "$OLD") $(has "$TMP/elsewhere")"

# A failed deploy (here: the early backup) leaves the old layout as it was, for the old bot.
old_layout "$GOOD_SET"
run inactive DPKG_OK=1 ROUTES_ACTIVE=1 AWG0_ACTIVE=1 BACKUP_FAIL=1
check "failed deploy: old state files kept" "last-backup maintenance ru-nets.stamp ru4.nft " "$(ls "$OLD" | tr '\n' ' ')"
printf 'BOT_TOKEN=1:a\nDB_SECRET_KEY=k\n' >"$R/etc/geoirb-vpn-bot/env"

check "never restarts routes or awg0" "0" "$(cat "$TMP"/all-calls | grep -c '^systemctl restart geoirb-\(vpn-routes\|awg0\)')"

finish
