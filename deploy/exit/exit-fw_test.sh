#!/usr/bin/env bash
# Tests deploy/exit/exit-fw.sh with fake ip/sysctl and a fake iptables that
# keeps the rules in a file ("<table> <chain> <rule>", top rule first) and
# fails like the real one (-C/-D of a missing rule).
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
cat >"$TMP/bin/iptables" <<'X'
#!/usr/bin/env bash
[[ "$1" == -t ]] || { echo "fake iptables: -t first" >&2; exit 2; }
t="$2"; op="$3"; chain="$4"; shift 4
line="$t $chain $*"
touch "$FW"
case "$op" in
  -C) grep -qxF -- "$line" "$FW" ;;
  -A) echo "$line" >>"$FW" ;;
  -I) { echo "$line"; cat "$FW"; } >"$FW.n"; mv "$FW.n" "$FW" ;;
  -D) grep -qxF -- "$line" "$FW" || exit 1
    awk -v l="$line" '!done && $0 == l { done = 1; next } { print }' "$FW" >"$FW.n"; mv "$FW.n" "$FW" ;;
  *) exit 2 ;;
esac
X
printf '#!/usr/bin/env bash\n[[ -n "${NO_ROUTE:-}" ]] || echo "default via 10.0.0.1 dev ens4 proto dhcp src 10.0.0.2 metric 100"\n' >"$TMP/bin/ip"
printf '#!/usr/bin/env bash\necho "sysctl $*" >>"$CALLS"\n' >"$TMP/bin/sysctl"
chmod +x "$TMP/bin"/*

FW="$TMP/rules"
run() { # run <up|down> [env...]
  local act="$1"; shift
  env FW="$FW" CALLS="$TMP/calls" PATH="$TMP/bin:$PATH" "$@" bash "$DIR/exit-fw.sh" "$act" awg-exit >"$TMP/out" 2>&1
}
OTHER="filter FORWARD -j DOCKER-USER" # somebody else's rule: never touched
WANT="filter FORWARD -i awg-exit -d 100.64.0.0/10 -j DROP
filter FORWARD -i awg-exit -d 169.254.0.0/16 -j DROP
filter FORWARD -i awg-exit -d 192.168.0.0/16 -j DROP
filter FORWARD -i awg-exit -d 172.16.0.0/12 -j DROP
filter FORWARD -i awg-exit -d 10.0.0.0/8 -j DROP
$OTHER
nat POSTROUTING -s 10.255.255.1 -o ens4 -j MASQUERADE
filter FORWARD -i awg-exit -j ACCEPT
filter FORWARD -o awg-exit -m state --state RELATED,ESTABLISHED -j ACCEPT
mangle FORWARD -o awg-exit -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu"

echo "$OTHER" >"$FW"
: >"$TMP/calls"
run up
check "up exits 0" "0" "$?"
check "up: NAT for the RU address only, private networks dropped above the accept, MSS clamp" "$WANT" "$(cat "$FW")"
check "up: forwarding on" "1" "$(grep -c '^sysctl -w net.ipv4.ip_forward=1$' "$TMP/calls")"
run up
check "up again: no duplicates" "0|$WANT" "$?|$(cat "$FW")"

# Leftovers of a broken run: a rule missing, another one twice, a DROP that
# ended up below the ACCEPT.
{
  echo "$OTHER"
  echo "filter FORWARD -i awg-exit -j ACCEPT"
  echo "filter FORWARD -i awg-exit -j ACCEPT"
  echo "filter FORWARD -i awg-exit -d 10.0.0.0/8 -j DROP"
} >"$FW"
run up
check "up after a broken run: every rule once, at its place" "0|$WANT" "$?|$(cat "$FW")"

: >"$TMP/calls"
run down
check "down: only our rules go" "0|$OTHER" "$?|$(cat "$FW")"
check "down: forwarding left on" "0" "$(grep -c ip_forward "$TMP/calls")"
run down
check "down again: nothing to remove is not an error" "0|$OTHER" "$?|$(cat "$FW")"

# One rule already gone: the rest is still removed (the old one-line PostDown stopped there).
run up
grep -vxF "filter FORWARD -i awg-exit -d 10.0.0.0/8 -j DROP" "$FW" >"$FW.n"; mv "$FW.n" "$FW"
run down
check "down with a rule missing: the rest is removed" "0|$OTHER" "$?|$(cat "$FW")"

run up NO_ROUTE=1
check "up without a default route fails, adds nothing" "1|$OTHER" "$([[ $? -ne 0 ]] && echo 1)|$(cat "$FW")"
run up
run down NO_ROUTE=1
check "down without a default route: forward rules still go" "0|$OTHER
nat POSTROUTING -s 10.255.255.1 -o ens4 -j MASQUERADE" "$?|$(cat "$FW")"

env PATH="$TMP/bin:$PATH" FW="$FW" bash "$DIR/exit-fw.sh" bogus awg-exit >/dev/null 2>&1
check "bad action fails" "1" "$?"
env PATH="$TMP/bin:$PATH" FW="$FW" bash "$DIR/exit-fw.sh" up >/dev/null 2>&1
check "no interface fails" "1" "$?"

echo
[[ "$FAILS" -eq 0 ]] && echo "all tests passed" || { echo "$FAILS failed"; exit 1; }
