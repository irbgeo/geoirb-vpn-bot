#!/usr/bin/env bash
# Tests deploy/import-peers.sh with ROOT=<tmp> and fake awg/awg-quick on PATH.
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

mkdir "$TMP/bin"
printf '#!/usr/bin/env bash\necho "awg $*" >>"$CALLS"\n[[ "$1" != syncconf ]] || cat "$3" >"$CALLS.synced"\n' >"$TMP/bin/awg"
printf '#!/usr/bin/env bash\ncat "$2"\n' >"$TMP/bin/awg-quick"
chmod +x "$TMP/bin"/*

CONF="$TMP/root/etc/amnezia/amneziawg/awg0.conf"
mkdir -p "$(dirname "$CONF")"
cat >"$CONF" <<'C'
[Interface]
Address = 10.8.0.1/22
PrivateKey = SERVERKEY

[Peer]
PublicKey = bbbbbbbbbb=
PresharedKey = psk-b
AllowedIPs = 10.8.0.3/32
C
chmod 640 "$CONF"
cat >"$TMP/old.conf" <<'C'
[Interface]
Address = 10.8.1.0/24
PrivateKey = OLDKEY

[Peer]
PublicKey = aaaaaaaaaa=
PresharedKey = psk-a
AllowedIPs = 10.8.1.2/32

[Peer]
PublicKey = bbbbbbbbbb=
PresharedKey = psk-b
AllowedIPs = 10.8.0.3/32

[Peer]
PublicKey = cccccccccc=
PresharedKey = psk-c
AllowedIPs = 10.8.1.4/32

[Peer]
PublicKey = dddddddddd=
PresharedKey = psk-d
AllowedIPs = 10.9.0.4/32
C

run() { CALLS="$TMP/calls" ROOT="$TMP/root" PATH="$TMP/bin:$PATH" bash "$DIR/import-peers.sh" "$TMP/old.conf" 2>&1; }

out="$(run)"
check "exit 0" "0" "$?"
check "summary" "imported 2, skipped 2" "$(tail -1 <<<"$out" | sed 's/ (.*//')"
check "peers in conf" "3" "$(grep -c '^\[Peer\]' "$CONF")"
check "duplicate once" "1" "$(grep -c 'bbbbbbbbbb=' "$CONF")"
check "new peers added" "2" "$(grep -c '^PublicKey = \(aaaaaaaaaa\|cccccccccc\)=$' "$CONF")"
check "PSK kept verbatim" "1" "$(grep -c '^PresharedKey = psk-c$' "$CONF")"
check "out-of-subnet skipped" "0" "$(grep -c 'dddddddddd' "$CONF")"
check "skipped listed by prefix" "1" "$(grep -c 'dddddddd' <<<"$out")"
check "no PSK printed" "0" "$(grep -c 'psk-' <<<"$out")"
check "interface untouched" "$(printf '[Interface]\nAddress = 10.8.0.1/22\nPrivateKey = SERVERKEY\n')" "$(sed -n 1,3p "$CONF")"
check "mode kept" "640" "$(mode "$CONF")"
check ".bak is the old conf" "1" "$(grep -c '^\[Peer\]' "$CONF.bak")"
check "no tmp left" "0" "$(ls "$(dirname "$CONF")" | grep -c tmp)"
check "one syncconf" "1" "$(grep -c '^awg syncconf awg0 ' "$TMP/calls")"
check "syncconf got the conf" "3" "$(grep -c '^\[Peer\]' "$TMP/calls.synced")"

cp "$CONF" "$TMP/after1"
: >"$TMP/calls"
out="$(run)"
check "second run imports 0" "imported 0, skipped 4" "$(tail -1 <<<"$out" | sed 's/ (.*//')"
check "second run keeps conf" "1" "$(cmp -s "$CONF" "$TMP/after1" && echo 1)"
check "second run one syncconf" "1" "$(grep -c '^awg syncconf' "$TMP/calls")"

rm "$CONF"
CALLS="$TMP/calls" ROOT="$TMP/root" PATH="$TMP/bin:$PATH" bash "$DIR/import-peers.sh" "$TMP/old.conf" >/dev/null 2>&1
check "missing new conf fails" "1" "$([[ $? -ne 0 ]] && echo 1)"

echo
[[ "$FAILS" -eq 0 ]] && echo "all tests passed" || { echo "$FAILS failed"; exit 1; }
