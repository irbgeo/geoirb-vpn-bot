#!/usr/bin/env bash
# Tests deploy/ru-nets.sh with a fixture RIPE file and fake curl/nft on PATH.
set -uo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
FAILS=0
check() { # check <name> <expected> <actual>
  if [[ "$2" == "$3" ]]; then echo "ok   $1"; else
    echo "FAIL $1"; echo "     expected: $2"; echo "     actual:   $3"; FAILS=$((FAILS + 1)); fi
}

cat >"$TMP/ripe" <<'X'
2|ripencc|20261008|123456|19830705|20261008|+0100
ripencc|*|ipv4|*|80000|summary
ripencc|RU|ipv4|5.8.0.0|8192|20100101|allocated
ripencc|RU|ipv4|5.10.0.0|768|20100101|allocated
ripencc|RU|ipv4|31.13.0.0|256|20100101|assigned
ripencc|DE|ipv4|2.16.0.0|4096|20100101|allocated
ripencc|RU|ipv6|2a00:1fa0::|32|20100101|allocated
ripencc|RU|asn|8359|1|20100101|allocated
X

mkdir "$TMP/bin"
# fake curl: writes the fixture to the -o file, or fails when $CURL_FAIL is set.
cat >"$TMP/bin/curl" <<'X'
#!/usr/bin/env bash
[[ -z "${CURL_FAIL:-}" ]] || exit 22
while [[ $# -gt 0 ]]; do [[ "$1" == -o ]] && out="$2"; shift; done
cp "$FIXTURE" "$out"
X
# fake nft: logs the call and keeps a copy of its -f input.
cat >"$TMP/bin/nft" <<'X'
#!/usr/bin/env bash
echo "nft $*" >>"$CALLS"
[[ "$1" == -f ]] && cp "$2" "$NFT_IN"
X
chmod +x "$TMP/bin"/*

STAMP="$TMP/root/var/lib/geoirb-vpn-bot/ru-nets.stamp"
run() { # run [env...]
  : >"$TMP/calls"; rm -f "$TMP/nft.in"
  env CALLS="$TMP/calls" NFT_IN="$TMP/nft.in" FIXTURE="$TMP/ripe" ROOT="$TMP/root" \
    PATH="$TMP/bin:$PATH" "$@" bash "$DIR/ru-nets.sh" >/dev/null 2>&1
}

run RU_NETS_MIN=3
check "exits 0" "0" "$?"
check "nft input" "flush set inet geoirb ru4
add element inet geoirb ru4 {
5.8.0.0/19,
5.10.0.0/23,
5.10.2.0/24,
31.13.0.0/24
}" "$(cat "$TMP/nft.in")"
check "nft called once" "1" "$(grep -c '^nft -f' "$TMP/calls")"
check "stamp touched" "1" "$([[ -f "$STAMP" ]] && echo 1)"
SAVED="$TMP/root/var/lib/geoirb-vpn-bot/ru4.nft"
check "elements saved for boot" "$(cat "$TMP/nft.in")" "$(cat "$SAVED" 2>/dev/null)"
check "saved file mode" "644" "$(stat -c %a "$SAVED" 2>/dev/null || stat -f %Lp "$SAVED")"
cp "$SAVED" "$TMP/saved.first"

touch -t 200001010000 "$STAMP"
run
check "short list fails" "1" "$?"
check "short list: nft not called" "0" "$(grep -c . "$TMP/calls")"
check "short list: stamp kept" "1" "$([[ "$STAMP" -ot "$TMP/ripe" ]] && echo 1)"

run RU_NETS_MIN=3 CURL_FAIL=1
check "failed download fails" "1" "$?"
check "failed download: nft not called" "0" "$(grep -c . "$TMP/calls")"
check "failed download: stamp kept" "1" "$([[ "$STAMP" -ot "$TMP/ripe" ]] && echo 1)"
check "failed runs keep the saved set" "1" "$(cmp -s "$SAVED" "$TMP/saved.first" && echo 1)"

echo
[[ "$FAILS" -eq 0 ]] && echo "all tests passed" || { echo "$FAILS failed"; exit 1; }
