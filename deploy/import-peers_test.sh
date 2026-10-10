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
# awg-quick strip: fails with $STRIP_FAIL, prints nothing with $STRIP_EMPTY.
printf '#!/usr/bin/env bash\n[[ -z "${STRIP_FAIL:-}" ]] || exit 1\n[[ -n "${STRIP_EMPTY:-}" ]] || cat "$2"\n' >"$TMP/bin/awg-quick"
printf '#!/usr/bin/env bash\necho "systemctl $*" >>"$CALLS"\n[[ "$1" != is-active ]] || [[ -n "${BOT_ACTIVE:-}" ]]\n' >"$TMP/bin/systemctl"
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

[Peer]
PublicKey = eeeeeeeeee=
PresharedKey = psk-e
AllowedIPs = 10.8.0.3/32

[Peer]
PublicKey = ffffffffff=
AllowedIPs = 10.8.1.9/32, 10.8.1.10/32

[Peer]
PublicKey = gggggggggg=
AllowedIPs = 10.8.1.0/24

[Peer]
PublicKey =
AllowedIPs = 10.8.1.11/32

[Peer]
PublicKey = hhhhhhhhhh=
AllowedIPs = 10.8.3.254/32
PersistentKeepalive = 25

[Peer]
PublicKey = kkkkkkkkkk=
AllowedIPs = 10.8.3.255/32

[Peer]
PublicKey = llllllllll=
AllowedIPs = 10.8.0.0/32

[Peer]
PublicKey = mmmmmmmmmm=
AllowedIPs = 10.8.0.1/32

[Peer]
PublicKey = iiiiiiiiii=
AllowedIPs = 10.8.4.0/32
C
# last block without a trailing newline, CRLF on one block
printf '\n[Peer]\r\nPublicKey = jjjjjjjjjj=\r\nAllowedIPs = 10.8.2.7/32' >>"$TMP/old.conf"

run() { env BOT_ACTIVE=1 CALLS="$TMP/calls" ROOT="$TMP/root" PATH="$TMP/bin:$PATH" "$@" bash "$DIR/import-peers.sh" "$TMP/old.conf" 2>&1; }

# A failed (or empty) `awg-quick strip` must not reach `awg syncconf`: an empty
# conf there removes every live peer. Nothing on the server changes.
cp "$CONF" "$TMP/before"
for sw in STRIP_FAIL=1 STRIP_EMPTY=1; do
  : >"$TMP/calls"
  run "$sw" >/dev/null
  check "$sw: fails" "1" "$([[ $? -ne 0 ]] && echo 1)"
  check "$sw: no syncconf" "0" "$(grep -c '^awg syncconf' "$TMP/calls")"
  check "$sw: conf untouched, nothing left behind" "awg0.conf" "$(cmp -s "$CONF" "$TMP/before" && ls "$(dirname "$CONF")")"
  check "$sw: bot started again" "1" "$(grep -c 'systemctl start geoirb-vpn-bot' "$TMP/calls")"
done
: >"$TMP/calls"

# The bot user can write to the conf directory: symlinks planted at fixed
# work-file names must not make root write through them.
echo keep >"$TMP/victim"
for n in tmp new skip; do ln -s "$TMP/victim" "$CONF.import.$n"; done
out="$(run)"
check "exit 0" "0" "$?"
check "planted symlinks are not written through" "keep" "$(cat "$TMP/victim")"
rm "$CONF".import.*
check "summary" "imported 4, skipped 10" "$(tail -1 <<<"$out" | sed 's/ (.*//')"
check "peers in conf" "5" "$(grep -c '^\[Peer\]' "$CONF")"
check "duplicate once" "1" "$(grep -c 'bbbbbbbbbb=' "$CONF")"
check "new peers added" "4" "$(grep -c '^PublicKey = \(aaaaaaaaaa\|cccccccccc\|hhhhhhhhhh\|jjjjjjjjjj\)=$' "$CONF")"
check "keepalive kept" "1" "$(grep -c '^PersistentKeepalive = 25$' "$CONF")"
check "/22 top in" "1" "$(grep -c '^AllowedIPs = 10.8.3.254/32$' "$CONF")"
check "broadcast, network and the server's own address out" "0 1 1 1" \
  "$(grep -c 'kkkkkkkkkk\|llllllllll\|mmmmmmmmmm' "$CONF") $(grep -c 'kkkkkkkk(subnet)' <<<"$out") $(grep -c 'llllllll(subnet)' <<<"$out") $(grep -c 'mmmmmmmm(ip-taken)' <<<"$out")"
check "/22 outside out" "0" "$(grep -c '10.8.4.0' "$CONF")"
check "ip-taken skipped" "0" "$(grep -c 'eeeeeeeeee' "$CONF")"
check "ip-taken listed" "1" "$(grep -c 'eeeeeeee(ip-taken)' <<<"$out")"
check "not-32 listed" "2" "$(grep -o '\(ffffffff\|gggggggg\)(not-32)' <<<"$out" | wc -l | tr -d ' ')"
check "empty key skipped" "1" "$(grep -c '(no-key)' <<<"$out")"
check "CRLF stripped" "0" "$(grep -c $'\r' "$CONF")"
check "last block imported" "1" "$(grep -c '^AllowedIPs = 10.8.2.7/32$' "$CONF")"
check "bot stopped then started" "systemctl stop geoirb-vpn-bot
systemctl start geoirb-vpn-bot" "$(grep -E 'systemctl (stop|start)' "$TMP/calls")"
check "PSK kept verbatim" "1" "$(grep -c '^PresharedKey = psk-c$' "$CONF")"
check "out-of-subnet skipped" "0" "$(grep -c 'dddddddddd' "$CONF")"
check "skipped listed by prefix" "1" "$(grep -c 'dddddddd' <<<"$out")"
check "no PSK printed" "0" "$(grep -c 'psk-' <<<"$out")"
check "interface untouched" "$(printf '[Interface]\nAddress = 10.8.0.1/22\nPrivateKey = SERVERKEY\n')" "$(sed -n 1,3p "$CONF")"
check "mode kept" "640" "$(mode "$CONF")"
check "one timestamped backup" "1" "$(ls "$CONF".bak-import-* | wc -l | tr -d ' ')"
check "backup is the old conf" "1" "$(grep -c '^\[Peer\]' "$CONF".bak-import-*)"
check "nothing left next to the conf but the backup" "awg0.conf awg0.conf.bak-import-" "$(ls "$(dirname "$CONF")" | sed 's/\(bak-import-\).*/\1/' | tr '\n' ' ' | sed 's/ $//')"
check "one syncconf" "1" "$(grep -c '^awg syncconf awg0 ' "$TMP/calls")"
check "syncconf got the conf" "5" "$(grep -c '^\[Peer\]' "$TMP/calls.synced")"

cp "$CONF" "$TMP/after1"
: >"$TMP/calls"
out="$(run)"
check "second run imports 0" "imported 0, skipped 14" "$(tail -1 <<<"$out" | sed 's/ (.*//')"
check "second run keeps conf" "1" "$(cmp -s "$CONF" "$TMP/after1" && echo 1)"
check "second run no syncconf" "0" "$(grep -c '^awg syncconf' "$TMP/calls")"
check "second run no new backup" "1" "$(ls "$CONF".bak-import-* | wc -l | tr -d ' ')"
check "bot restarted after 0 import" "1" "$(grep -c 'systemctl start geoirb-vpn-bot' "$TMP/calls")"

# The bot was not running: it is neither stopped nor started.
: >"$TMP/calls"
run BOT_ACTIVE= >/dev/null
check "bot not running: exit 0, left alone" "0 0" "$? $(grep -cE 'systemctl (stop|start)' "$TMP/calls")"

rm "$CONF"
CALLS="$TMP/calls" ROOT="$TMP/root" PATH="$TMP/bin:$PATH" bash "$DIR/import-peers.sh" "$TMP/old.conf" >/dev/null 2>&1
check "missing new conf fails" "1" "$([[ $? -ne 0 ]] && echo 1)"

echo
[[ "$FAILS" -eq 0 ]] && echo "all tests passed" || { echo "$FAILS failed"; exit 1; }
