#!/usr/bin/env bash
# Tests scripts/tunnel-keys.sh and scripts/render-tunnel.sh in a temp dir.
set -uo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
FAILS=0
check() { # check <name> <expected> <actual>
  if [[ "$2" == "$3" ]]; then echo "ok   $1"; else
    echo "FAIL $1"; echo "     expected: $2"; echo "     actual:   $3"; FAILS=$((FAILS + 1)); fi
}
export TUNNEL_FILE="$TMP/secret/tunnel.yaml"
val() { awk -v k="$1" '$1 == k":" { sub(/^[^:]*:[[:space:]]*/, ""); print; exit }' "$TUNNEL_FILE"; }
mode() { stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1"; }
bytes() { printf '%s' "$1" | base64 -d 2>/dev/null | wc -c | tr -d ' '; }
# pub_of <private base64> — public key re-derived with openssl.
pub_of() {
  { printf '302e020100300506032b656e04220420' | xxd -r -p; printf '%s' "$1" | base64 -d; } |
    openssl pkey -inform DER -pubout -outform DER | tail -c 32 | base64
}

"$DIR/tunnel-keys.sh" >/dev/null
check "file mode" "600" "$(mode "$TUNNEL_FILE")"
for k in ru_private ru_public exit_private exit_public psk; do
  check "$k is 32 bytes" "32" "$(bytes "$(val "$k")")"
done
check "ru public matches private" "$(val ru_public)" "$(pub_of "$(val ru_private)")"
check "exit public matches private" "$(val exit_public)" "$(pub_of "$(val exit_private)")"
check "ru and exit keys differ" "1" "$([[ "$(val ru_private)" != "$(val exit_private)" ]] && echo 1)"

port="$(val port)"
check "port range" "1" "$([[ "$port" -ge 20000 && "$port" -le 60000 ]] && echo 1)"
jc="$(val jc)"
check "jc range" "1" "$([[ "$jc" -ge 4 && "$jc" -le 8 ]] && echo 1)"
check "jmin" "40" "$(val jmin)"
check "jmax" "70" "$(val jmax)"
s1="$(val s1)"; s2="$(val s2)"
check "s1 range" "1" "$([[ "$s1" -ge 15 && "$s1" -le 60 ]] && echo 1)"
check "s2 range" "1" "$([[ "$s2" -ge 15 && "$s2" -le 60 ]] && echo 1)"
check "s1+56 != s2" "1" "$([[ $((s1 + 56)) -ne "$s2" ]] && echo 1)"
hs="$(for i in 1 2 3 4; do val "h$i"; done)"
check "h1..h4 distinct" "4" "$(echo "$hs" | sort -u | wc -l | tr -d ' ')"
check "h1..h4 > 4" "0" "$(echo "$hs" | awk '$1 <= 4 || $1 > 4294967295' | wc -l | tr -d ' ')"

before="$(cat "$TUNNEL_FILE")"
"$DIR/tunnel-keys.sh" >/dev/null
check "second run leaves the file untouched" "$before" "$(cat "$TUNNEL_FILE")"

ru="$("$DIR/render-tunnel.sh" ru 1.2.3.4)"
ex="$("$DIR/render-tunnel.sh" exit 1.2.3.4)"
check "ru endpoint" "1" "$(grep -qx "Endpoint = 1.2.3.4:$port" <<<"$ru" && echo 1)"
check "ru has exit public key" "1" "$(grep -q "$(val exit_public)" <<<"$ru" && echo 1)"
check "ru has no exit private key" "" "$(grep -F "$(val exit_private)" <<<"$ru")"
check "ru address" "1" "$(grep -qx 'Address = 10.255.255.1/30' <<<"$ru" && echo 1)"
check "ru Table off" "1" "$(grep -qx 'Table = off' <<<"$ru" && echo 1)"
check "ru has no PostUp" "" "$(grep PostUp <<<"$ru")"
check "ru obfuscation" "1" "$(grep -qx "S3 = 0" <<<"$ru" && grep -qx "H4 = $(val h4)" <<<"$ru" && echo 1)"
check "exit has no ru private key" "" "$(grep -F "$(val ru_private)" <<<"$ex")"
check "exit has ru public key" "1" "$(grep -q "$(val ru_public)" <<<"$ex" && echo 1)"
check "exit listen port" "1" "$(grep -qx "ListenPort = $port" <<<"$ex" && echo 1)"
check "exit allowed ips" "1" "$(grep -qx 'AllowedIPs = 10.255.255.1/32' <<<"$ex" && echo 1)"
check "exit has PostUp" "1" "$(grep -q '^PostUp = sysctl -w net.ipv4.ip_forward=1;.*MASQUERADE' <<<"$ex" && echo 1)"
check "exit PostDown keeps ip_forward" "" "$(grep '^PostDown' <<<"$ex" | grep ip_forward)"
check "exit PostDown deletes" "1" "$(grep '^PostDown' <<<"$ex" | grep -q -- '-D FORWARD' && echo 1)"

"$DIR/render-tunnel.sh" bogus 1.2.3.4 >/dev/null 2>&1
check "bad side fails" "1" "$?"

echo
[[ "$FAILS" -eq 0 ]] && echo "all tests passed" || { echo "$FAILS failed"; exit 1; }
