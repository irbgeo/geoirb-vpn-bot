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
# nft: logs; fails on the saved set when $BAD_SET is set.
printf '#!/usr/bin/env bash\necho "nft $*" >>"$CALLS"\n[[ -z "${BAD_SET:-}" || "$2" != *ru4.nft ]]\n' >"$TMP/bin/nft"
# ip: logs; `rule show` prints the rule when $HAS_RULE is set.
printf '#!/usr/bin/env bash\necho "ip $*" >>"$CALLS"\n[[ "$1 $2" != "rule show" || -z "${HAS_RULE:-}" ]] || echo "110:\tfrom all fwmark 0x1 lookup 100"\n' >"$TMP/bin/ip"
chmod +x "$TMP/bin"/*

R="$TMP/root"
mkdir -p "$R/etc/geoirb-vpn" "$R/var/lib/geoirb-vpn-bot"
touch "$R/etc/geoirb-vpn/geoirb-vpn.nft"
run() { : >"$TMP/calls"; env CALLS="$TMP/calls" ROOT="$R" PATH="$TMP/bin:$PATH" "$@" bash "$DIR/vpn-routes.sh" >/dev/null 2>&1; }
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

echo
[[ "$FAILS" -eq 0 ]] && echo "all tests passed" || { echo "$FAILS failed"; exit 1; }
