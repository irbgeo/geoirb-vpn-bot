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

# `make deploy` (RU side) must never create the tunnel keys: it stops before
# the build, with no ssh. The access file is a fake, ssh and make are fakes.
mkdir "$TMP/fake"
for c in make ssh sshpass; do printf '#!/bin/sh\necho "%s $*" >>"%s"\n' "$c" "$TMP/fake/calls" >"$TMP/fake/$c"; done
chmod +x "$TMP/fake"/*
printf 'host: ru.example\nuser: u\n' >"$TMP/access.yaml"
err="$(PATH="$TMP/fake:$PATH" ACCESS_FILE="$TMP/access.yaml" bash "$DIR/deploy.sh" 2>&1 >/dev/null)"
check "deploy without tunnel.yaml fails" "1" "$?"
check "deploy without tunnel.yaml says why" "1" "$(grep -c "error: $TUNNEL_FILE is missing" <<<"$err")"
check "deploy without tunnel.yaml creates no keys, builds nothing, no ssh" "0 0" "$([[ -e "$TUNNEL_FILE" ]] && echo 1 || echo 0) $([[ -e "$TMP/fake/calls" ]] && echo 1 || echo 0)"

out="$("$DIR/tunnel-keys.sh")"
check "a new file is announced" "1" "$(grep -c "^created $TUNNEL_FILE" <<<"$out")"
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
out="$("$DIR/tunnel-keys.sh")"
check "second run leaves the file untouched, says nothing" "$before|" "$(cat "$TUNNEL_FILE")|$out"

ru="$("$DIR/render-tunnel.sh" ru 1.2.3.4)"
ex="$("$DIR/render-tunnel.sh" exit 1.2.3.4)"
check "ru endpoint" "1" "$(grep -qx "Endpoint = 1.2.3.4:$port" <<<"$ru" && echo 1)"
check "ru has exit public key" "1" "$(grep -q "$(val exit_public)" <<<"$ru" && echo 1)"
check "ru has no exit private key" "" "$(grep -F "$(val exit_private)" <<<"$ru")"
check "ru address" "1" "$(grep -qx 'Address = 10.255.255.1/30' <<<"$ru" && echo 1)"
check "ru Table off" "1" "$(grep -qx 'Table = off' <<<"$ru" && echo 1)"
check "ru PostUp is the table 100 route only" "PostUp = ip route replace default dev %i table 100" "$(grep -E '^(PostUp|PostDown|PreUp|PreDown)' <<<"$ru")"
check "ru has no uid rules" "" "$(grep -E 'uidrange|ip rule' <<<"$ru")"
check "ru obfuscation" "1" "$(grep -qx "S3 = 0" <<<"$ru" && grep -qx "H4 = $(val h4)" <<<"$ru" && echo 1)"
check "exit has no ru private key" "" "$(grep -F "$(val ru_private)" <<<"$ex")"
check "exit has ru public key" "1" "$(grep -q "$(val ru_public)" <<<"$ex" && echo 1)"
check "exit listen port" "1" "$(grep -qx "ListenPort = $port" <<<"$ex" && echo 1)"
check "exit allowed ips" "1" "$(grep -qx 'AllowedIPs = 10.255.255.1/32' <<<"$ex" && echo 1)"
# the rules themselves: deploy/exit/exit-fw.sh and its test
check "exit hooks run the firewall script" "PostUp = /usr/local/sbin/geoirb-exit-fw.sh up %i
PostDown = /usr/local/sbin/geoirb-exit-fw.sh down %i" "$(grep -E '^(PostUp|PostDown|PreUp|PreDown)' <<<"$ex")"

odd="$("$DIR/render-tunnel.sh" ru 'a&b|c\1')"
check "host with & | \\ is copied as is" "Endpoint = a&b|c\\1:$port" "$(grep '^Endpoint' <<<"$odd")"
check "only the Endpoint line depends on the host" "$(grep -v '^Endpoint' <<<"$ru")" "$(grep -v '^Endpoint' <<<"$odd")"

grep -v '^psk:' "$TUNNEL_FILE" >"$TMP/nopsk.yaml"
TUNNEL_FILE="$TMP/nopsk.yaml" "$DIR/render-tunnel.sh" ru 1.2.3.4 >/dev/null 2>&1
check "missing key fails" "1" "$?"
: >"$TMP/empty.yaml"
out="$(TUNNEL_FILE="$TMP/empty.yaml" "$DIR/render-tunnel.sh" ru 1.2.3.4 2>/dev/null)"
check "empty tunnel.yaml fails, prints no conf" "1|" "$?|$out"

# A failing openssl must leave neither tunnel.yaml nor its .tmp behind.
mkdir -p "$TMP/bin"
real="$(command -v openssl)"
printf '#!/bin/sh\n[ "$1" = rand ] && exit 1\nexec "%s" "$@"\n' "$real" >"$TMP/bin/openssl"
chmod +x "$TMP/bin/openssl"
PATH="$TMP/bin:$PATH" TUNNEL_FILE="$TMP/fail/tunnel.yaml" "$DIR/tunnel-keys.sh" >/dev/null 2>&1
check "failed run exits non-zero" "1" "$([[ $? -ne 0 ]] && echo 1)"
check "failed run leaves no files" "" "$(ls -A "$TMP/fail" 2>/dev/null)"


"$DIR/render-tunnel.sh" bogus 1.2.3.4 >/dev/null 2>&1
check "bad side fails" "1" "$?"

echo
[[ "$FAILS" -eq 0 ]] && echo "all tests passed" || { echo "$FAILS failed"; exit 1; }
