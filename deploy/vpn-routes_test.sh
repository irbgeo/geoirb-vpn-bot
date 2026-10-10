#!/usr/bin/env bash
# Tests deploy/vpn-routes.sh with ROOT=<tmp> and fake nft/ip on PATH.
set -uo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
FAILS=0
check() { # check <name> <expected> <actual>
  if [[ "$2" == "$3" ]]; then echo "ok   $1"; else
    echo "FAIL $1"; echo "     expected: $2"; echo "     actual:   $3"; FAILS=$((FAILS + 1)); fi
}

mkdir "$TMP/bin"
# nft: logs; fails on the saved set when $BAD_SET is set; `list chain` prints
# the mark rule as real nft does when $HAS_TABLE is set, else fails (no table).
cat >"$TMP/bin/nft" <<'EOF2'
#!/usr/bin/env bash
echo "nft $*" >>"$CALLS"
if [[ "$1" == list ]]; then
  [[ -n "${HAS_TABLE:-}" ]] || { echo "Error: No such file or directory" >&2; exit 1; }
  printf 'table inet geoirb {\n\tchain pre {\n\t\ttype filter hook prerouting priority mangle; policy accept;\n'
  printf '\t\tiifname "awg0" ip daddr != @ru4 ip daddr != 10.8.0.0/22 meta mark set 0x00000001\n\t}\n}\n'
  exit
fi
[[ -z "${BAD_SET:-}" || "$2" != *ru4.nft ]]
EOF2
# ip: logs; `rule show` prints the rule when $HAS_RULE is set.
cat >"$TMP/bin/ip" <<'EOF2'
#!/usr/bin/env bash
echo "ip $*" >>"$CALLS"
if [[ "$1 $2" == "link show" ]]; then [[ -n "${HAS_EXIT:-}" ]]; exit; fi
# the tunnel link exists but is not up yet: the kernel refuses the route
if [[ "$*" == "route replace default dev awg-exit table 100" && -n "${EXIT_DOWN:-}" ]]; then echo "RTNETLINK answers: Network is down" >&2; exit 2; fi
[[ "$1 $2" != "rule show" || -z "${HAS_RULE:-}" ]] || echo "110:	from all fwmark 0x1 lookup 100"
EOF2
chmod +x "$TMP/bin"/*

R="$TMP/root"
mkdir -p "$R/etc/geoirb-vpn" "$R/var/lib/geoirb-vpn-bot"
touch "$R/etc/geoirb-vpn/geoirb-vpn.nft"
MODE=""
run() { : >"$TMP/calls"; env CALLS="$TMP/calls" ROOT="$R" PATH="$TMP/bin:$PATH" "$@" bash "$DIR/vpn-routes.sh" $MODE >/dev/null 2>"$TMP/err"; }
n() { grep -c "$1" "$TMP/calls"; }

run
check "no saved set: exits 0" "0" "$?"
check "table loaded" "1" "$(n "^nft -f $R/etc/geoirb-vpn/geoirb-vpn.nft$")"
check "no saved set: not loaded" "0" "$(n 'ru4.nft')"
check "unreachable route" "1" "$(n '^ip route replace unreachable default metric 4096 table 100$')"
check "rule added when missing" "1" "$(n '^ip rule add fwmark 0x1 lookup 100 prio 110$')"
check "rule never deleted" "0" "$(n '^ip rule del')"

echo "add element inet geoirb ru4 { 5.8.0.0/19 }" >"$R/var/lib/geoirb-vpn-bot/ru4.nft"
run HAS_RULE=1
check "rule present: exits 0" "0" "$?"
check "saved set loaded after the table" "2" "$(grep -n '^nft -f' "$TMP/calls" | grep ru4.nft | cut -d: -f1)"
check "rule present: not added again" "0" "$(n '^ip rule add')"

run BAD_SET=1
check "bad saved set tolerated" "0" "$?"
check "bad saved set: rule and route still set" "1 1" "$(n '^ip rule add') $(n '^ip route replace unreachable')"

# The exit route: networkd or anything else may drop it while the tunnel is up.
run
check "no tunnel yet: exit route not set" "0" "$(n '^ip route replace default dev awg-exit table 100$')"
run HAS_EXIT=1
check "tunnel up: exit route ensured" "1" "$(n '^ip route replace default dev awg-exit table 100$')"

run HAS_EXIT=1 EXIT_DOWN=1
check "tunnel link not up yet: not fatal" "0" "$?"
check "tunnel link not up yet: rule and unreachable still set" "1 1" "$(n '^ip rule add') $(n '^ip route replace unreachable')"

# `rules` mode (the every-minute check): only ip rule/routes, no nft reload.
MODE=rules
run HAS_EXIT=1 HAS_TABLE=1
check "rules mode: exits 0" "0" "$?"
check "rules mode: nft only looked at, not loaded" "1 0" "$(n '^nft list chain inet geoirb pre$') $(n '^nft -f')"
check "rules mode: rule, unreachable and exit route ensured" "1 1 1" \
  "$(n '^ip rule add fwmark 0x1 lookup 100 prio 110$') $(n '^ip route replace unreachable default metric 4096 table 100$') $(n '^ip route replace default dev awg-exit table 100$')"
check "rules mode: a missing rule is reported" "1" "$(grep -c 'warning: split-routing rule was missing' "$TMP/err")"
run HAS_EXIT=1 HAS_RULE=1 HAS_TABLE=1
check "rules mode: nothing deleted, rule not re-added" "0 0" "$(n '^ip rule del') $(n '^ip rule add')"
check "rules mode: quiet when all is in place" "0" "$(wc -c <"$TMP/err" | tr -d ' ')"
# The nft table is gone (`nft flush ruleset`): loaded again with the saved set.
run HAS_EXIT=1 HAS_RULE=1
check "rules mode, no nft table: exits 0" "0" "$?"
check "rules mode, no nft table: table and saved set loaded" "nft -f $R/etc/geoirb-vpn/geoirb-vpn.nft
nft -f $R/var/lib/geoirb-vpn-bot/ru4.nft" "$(grep '^nft -f' "$TMP/calls")"
check "rules mode, no nft table: reported" "1" "$(grep -c 'warning: nft table inet geoirb was missing' "$TMP/err")"
MODE=""

# The check unit hides systemd's "Started/Finished" each minute, not the script's errors.
CU="$DIR/geoirb-vpn-routes-check.service"
check "check unit: quiet start/stop, script stderr still logged" "1 1" "$(grep -cx 'LogLevelMax=notice' "$CU") $(grep -cx 'SyslogLevel=warning' "$CU")"

# The real nft file: clients may not reach each other or private networks.
NFT="${NFT_FILE:-$DIR/geoirb-vpn.nft}"
check "vpn_forward chain flushed on reload" "1" "$(grep -cx 'flush chain inet geoirb vpn_forward' "$NFT")"
check "vpn_forward hook" "1" "$(grep -c 'type filter hook forward priority filter;' "$NFT")"
check "loopback is never masqueraded" "1" "$(grep -c 'oifname != "lo"' "$NFT")"
check "client to client dropped" "1" "$(grep -cx $'\t\tiifname "awg0" oifname "awg0" drop' "$NFT")"
check "marked traffic not leaving through the tunnel is dropped" "1" "$(grep -cx $'\t\tiifname "awg0" meta mark 0x1 oifname != "awg-exit" drop' "$NFT")"
check "private ranges dropped" "1" "$(grep -cx $'\t\tiifname "awg0" ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, 100.64.0.0/10 } drop' "$NFT")"

echo
[[ "$FAILS" -eq 0 ]] && echo "all tests passed" || { echo "$FAILS failed"; exit 1; }
