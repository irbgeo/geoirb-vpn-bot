#!/usr/bin/env bash
# Runs as root on the RU server (geoirb-ru-nets.timer: weekly and at boot).
# Fills the nftables set `inet geoirb ru4` (deploy/geoirb-vpn.nft) with the
# Russian IPv4 networks from the RIPE NCC delegated stats: client traffic to
# them leaves here directly, the rest goes through the exit tunnel. A failed
# or short download keeps the old set and exits 1. On success it touches the
# stamp the bot watches. ROOT is a path prefix for tests.
set -euo pipefail
URL="${RU_NETS_URL:-https://ftp.ripe.net/pub/stats/ripencc/delegated-ripencc-latest}"
MIN="${RU_NETS_MIN:-1000}" # fewer prefixes = a broken download
STATE="${ROOT:-}/var/lib/geoirb-vpn-bot"
STAMP="$STATE/ru-nets.stamp"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if ! curl -fsS --max-time 60 --retry 3 -o "$tmp/ripe" "$URL"; then
  echo "ru-nets: download of $URL failed, the old set is kept" >&2
  exit 1
fi

# A row is start|count; count is a power of two in practice, otherwise it is
# split into the largest aligned power-of-two blocks.
awk -F'|' '$2 == "RU" && $3 == "ipv4" {
  split($4, o, ".")
  start = ((o[1] * 256 + o[2]) * 256 + o[3]) * 256 + o[4]
  left = $5
  while (left > 0) {
    size = 1; bits = 32
    while (size * 2 <= left && start % (size * 2) == 0) { size *= 2; bits-- }
    printf "%d.%d.%d.%d/%d\n", int(start / 16777216) % 256, int(start / 65536) % 256, int(start / 256) % 256, start % 256, bits
    start += size; left -= size
  }
}' "$tmp/ripe" >"$tmp/cidrs"

n="$(wc -l <"$tmp/cidrs" | tr -d ' ')"
if [[ "$n" -lt "$MIN" ]]; then
  echo "ru-nets: only $n RU prefixes (want >= $MIN), the old set is kept" >&2
  exit 1
fi

# One nft transaction: the set is never seen empty.
{
  echo "flush set inet geoirb ru4"
  echo "add element inet geoirb ru4 {"
  sed '$!s/$/,/' "$tmp/cidrs"
  echo "}"
} >"$tmp/ru4.nft"
nft -f "$tmp/ru4.nft"

# Kept for the next boot: geoirb-vpn-routes.service loads it before the tunnels.
mkdir -p "$STATE"
install -m 644 "$tmp/ru4.nft" "$STATE/ru4.nft"
touch "$STAMP"
echo "ru-nets: $n RU prefixes loaded"
