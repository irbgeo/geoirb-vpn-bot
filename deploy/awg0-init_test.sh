#!/usr/bin/env bash
# Tests deploy/awg0-init.sh with ROOT=<tmp> and fake awg/chown on PATH.
set -uo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
FAILS=0
check() { # check <name> <expected> <actual>
  if [[ "$2" == "$3" ]]; then echo "ok   $1"; else
    echo "FAIL $1"; echo "     expected: $2"; echo "     actual:   $3"; FAILS=$((FAILS + 1)); fi
}
mode() { stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1"; }
val() { sed -n "s/^$1 = //p" "$CONF"; }

mkdir "$TMP/bin"
printf '#!/usr/bin/env bash\n[[ "$1" == genkey ]] && echo "c2VydmVyLXByaXZhdGUta2V5LWZvci10ZXN0cy0xMjM0NTY="\n' >"$TMP/bin/awg"
printf '#!/usr/bin/env bash\necho "chown $*" >>"$CALLS"\n' >"$TMP/bin/chown"
chmod +x "$TMP/bin"/*

run() { CALLS="$TMP/calls" ROOT="$TMP/root" PATH="$TMP/bin:$PATH" bash "$DIR/awg0-init.sh"; }
CONF="$TMP/root/etc/amnezia/amneziawg/awg0.conf"

out="$(run)"
check "first run exits 0" "0" "$?"
check "first run says created" "1" "$(grep -c created <<<"$out")"
check "conf mode" "600" "$(mode "$CONF")"
check "dir mode" "770" "$(mode "$(dirname "$CONF")")"
check "dir owner" "1" "$(grep -c "^chown root:vpnbot $TMP/root/etc/amnezia/amneziawg$" "$TMP/calls")"
check "conf owner" "1" "$(grep -c "^chown vpnbot:vpnbot $CONF$" "$TMP/calls")"
check "first line" "[Interface]" "$(head -1 "$CONF")"
check "Address" "10.8.0.1/22" "$(val Address)"
check "ListenPort" "443" "$(val ListenPort)"
check "MTU" "1380" "$(val MTU)"
check "PrivateKey" "c2VydmVyLXByaXZhdGUta2V5LWZvci10ZXN0cy0xMjM0NTY=" "$(val PrivateKey)"
jc="$(val Jc)"
check "Jc 4-8" "1" "$([[ "$jc" -ge 4 && "$jc" -le 8 ]] && echo 1)"
check "Jmin" "40" "$(val Jmin)"
check "Jmax" "70" "$(val Jmax)"
s1="$(val S1)"; s2="$(val S2)"; s3="$(val S3)"; s4="$(val S4)"
check "S1 15-60" "1" "$([[ "$s1" -ge 15 && "$s1" -le 60 ]] && echo 1)"
check "S2 15-60" "1" "$([[ "$s2" -ge 15 && "$s2" -le 60 ]] && echo 1)"
check "S1+56 != S2" "1" "$([[ $((s1 + 56)) -ne "$s2" ]] && echo 1)"
check "S3 8-32" "1" "$([[ "$s3" -ge 8 && "$s3" -le 32 ]] && echo 1)"
check "S4 8-32" "1" "$([[ "$s4" -ge 8 && "$s4" -le 32 ]] && echo 1)"
# H1-H4: ranges a-b, a > 4, a < b <= 2^32-1, not overlapping.
hs="$(for h in H1 H2 H3 H4; do val "$h"; done)"
check "H1-H4 are ranges" "4" "$(grep -cE '^[0-9]+-[0-9]+$' <<<"$hs")"
check "H ranges valid and apart" "1" "$(tr - ' ' <<<"$hs" | sort -n | awk '
  $1 <= 4 || $2 <= $1 || $2 > 4294967295 || $1 <= prev { bad = 1 } { prev = $2 } END { if (!bad) print 1 }')"
check "I1 commented, QUIC-like" "1" "$(grep -cE '^# I1 = <b 0x(c[0-3]00000001)[0-9a-f]+>$' "$CONF")"
i1len="$(sed -n 's/^# I1 = <b 0x\([0-9a-f]*\)>$/\1/p' "$CONF" | tr -d '\n' | wc -c | tr -d ' ')"
check "I1 is 64-128 bytes" "1" "$([[ "$i1len" -ge 128 && "$i1len" -le 256 ]] && echo 1)"
check "no active I1" "0" "$(grep -c '^I1' "$CONF")"

cp "$CONF" "$TMP/first"

# awg0-check.sh (ExecStartPre/ExecStop of geoirb-awg0.service): the conf is the
# bot's, so a line awg-quick would run as root must stop the unit.
bash "$DIR/awg0-check.sh" "$CONF" 2>/dev/null
check "check: a fresh conf passes" "0" "$?"
for l in 'PostUp = id' '  postup=id' 'PreUp = id' 'PreDown = id' 'PostDown = id' 'SaveConfig = true'; do
  { cat "$CONF"; echo "$l"; } >"$TMP/hook.conf"
  bash "$DIR/awg0-check.sh" "$TMP/hook.conf" 2>/dev/null
  check "check: '$l' is refused" "1" "$?"
done
{ cat "$CONF"; echo "# PostUp = id"; } >"$TMP/hook.conf"
bash "$DIR/awg0-check.sh" "$TMP/hook.conf" 2>/dev/null
check "check: a commented hook passes" "0" "$?"
bash "$DIR/awg0-check.sh" "$TMP/none.conf" 2>/dev/null
check "check: a missing conf fails" "1" "$([[ $? -ne 0 ]] && echo 1)"
U="$DIR/geoirb-awg0.service"
check "unit: the check is the first ExecStartPre and ExecStop, not optional" "ExecStartPre=/opt/geoirb-vpn-bot/awg0-check.sh /etc/amnezia/amneziawg/awg0.conf
ExecStop=/opt/geoirb-vpn-bot/awg0-check.sh /etc/amnezia/amneziawg/awg0.conf" "$(grep -m1 '^ExecStartPre=' "$U"; grep -m1 '^ExecStop=' "$U")"

# awg genkey fails: no conf with an empty key.
printf '#!/usr/bin/env bash\nexit 1\n' >"$TMP/bin/awg"
rm "$CONF"
run >/dev/null 2>&1
check "genkey failure fails" "1" "$([[ $? -ne 0 ]] && echo 1)"
check "genkey failure: no conf" "0" "$([[ -e "$CONF" ]] && echo 1 || echo 0)"
cp "$TMP/first" "$CONF"
out="$(run)"
check "second run exits 0" "0" "$?"
check "second run says kept" "1" "$(grep -c kept <<<"$out")"
check "second run keeps the conf" "1" "$(cmp -s "$CONF" "$TMP/first" && echo 1)"

echo
[[ "$FAILS" -eq 0 ]] && echo "all tests passed" || { echo "$FAILS failed"; exit 1; }
