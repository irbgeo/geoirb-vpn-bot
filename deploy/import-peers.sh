#!/usr/bin/env bash
# Usage: import-peers.sh <old.conf>. Runs as root on the RU server.
# Appends the [Peer] blocks of the old server's conf to awg0.conf, verbatim,
# when their PublicKey is not there yet and AllowedIPs is one IPv4 /32 inside
# the Address subnet of awg0.conf (not its network or broadcast address) and
# not used by another peer or by the server; then applies them live (no
# client drops).
# The running bot also rewrites awg0.conf (its sha check cannot see this
# script), so the bot is stopped first and started again on exit, if it ran.
# ROOT is a path prefix for tests.
set -euo pipefail
umask 077
OLD="${1:?usage: import-peers.sh <old.conf>}"
CONF="${ROOT:-}/etc/amnezia/amneziawg/awg0.conf"
BOT=geoirb-vpn-bot
restart=0
# Work files live in a root-only directory, not next to the conf: the bot
# user can write there, and a fixed name could be a symlink it planted for
# root to write through. The name awg0.conf is what `awg-quick strip` wants.
tmp="$(mktemp -d)"
W="" # the one file made next to the conf, under a random name
cleanup() {
  rm -rf "$tmp"
  [[ -z "$W" ]] || rm -f "$W"
  [[ "$restart" == 0 ]] || systemctl start "$BOT"
}
trap cleanup EXIT

if systemctl is-active --quiet "$BOT"; then
  systemctl stop "$BOT"
  restart=1
fi

# Pass 1 reads the new conf (keys, taken IPs, subnet), pass 2 the old one.
# Blocks to import go to $tmp/new; the summary (skipped keys by 8-char prefix,
# never the PSK) to $tmp/skip.
awk -v new="$CONF" -v skipfile="$tmp/skip" '
function ip2n(s,  a) { split(s, a, "."); return ((a[1] * 256 + a[2]) * 256 + a[3]) * 256 + a[4] }
function skip(why) { skipped++; skiplist = skiplist " " substr(key, 1, 8) "(" why ")" }
function flush(  n, ips, a, ip) {
  if (sect != "Peer") { blk = ""; return }
  n = split(ips_s, ips, ",")
  gsub(/[ \t]/, "", ips[2])
  split(ips[2], a, "/")
  ip = a[1]
  if (key == "") skip("no-key")
  else if (key in seen) skip("dup")
  else if (n != 2 || ips[2] !~ /^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+\/32$/) skip("not-32")
  else if (ip2n(ip) <= base || ip2n(ip) >= base + size - 1) skip("subnet") # also the network and broadcast addresses
  else if (ip in taken) skip("ip-taken")
  else { imported++; seen[key] = 1; taken[ip] = 1; printf "\n%s", blk }
  blk = ""
}
BEGIN {
  while ((getline l < new) > 0) {
    gsub(/[ \t\r]+$/, "", l)
    if (l ~ /^\[/) nsect = substr(l, 2, index(l, "]") - 2)
    if (l ~ /^PublicKey[ \t]*=/) { sub(/^[^=]*=[ \t]*/, "", l); seen[l] = 1 }
    else if (l ~ /^AllowedIPs[ \t]*=/ && nsect == "Peer") {
      sub(/^[^=]*=[ \t]*/, "", l); m = split(l, parts, ",")
      for (i = 1; i <= m; i++) { gsub(/[ \t]/, "", parts[i]); split(parts[i], a, "/"); taken[a[1]] = 1 }
    }
    else if (l ~ /^Address[ \t]*=/) { sub(/^[^=]*=[ \t]*/, "", l); split(l, a, "/")
      size = 2 ^ (32 - a[2]); base = int(ip2n(a[1]) / size) * size; taken[a[1]] = 1 } # the server itself
  }
  if (size == 0) { print "error: no Address in " new > "/dev/stderr"; exit 1 }
}
{ sub(/\r$/, "") }
/^\[/ { flush(); sect = substr($0, 2, index($0, "]") - 2); key = ""; ips_s = "" }
sect == "Peer" {
  if ($0 ~ /^PublicKey[ \t]*=/) { key = $0; sub(/^[^=]*=[ \t]*/, "", key); gsub(/[ \t]+$/, "", key) }
  if ($0 ~ /^AllowedIPs[ \t]*=/) { v = $0; sub(/^[^=]*=[ \t]*/, "", v); ips_s = ips_s "," v }
  if ($0 !~ /^[ \t]*$/) blk = blk $0 "\n"
}
END { flush(); print "imported " imported + 0 ", skipped " skipped + 0 (skiplist != "" ? " (" substr(skiplist, 2) ")" : "") > skipfile }
' "$OLD" >"$tmp/new"

summary="$(cat "$tmp/skip")"
echo "$summary"
n="${summary#imported }"
n="${n%%,*}"
[[ "$n" != 0 ]] || exit 0

cp -p "$CONF" "$tmp/awg0.conf" # same owner and mode
[[ -z "$(tail -c1 "$CONF")" ]] || echo >>"$tmp/awg0.conf"
cat "$tmp/new" >>"$tmp/awg0.conf"
# Strip into a file, and before the conf is replaced: bash does not see a
# failure inside <(...), and `awg syncconf` with an empty file would remove
# every live peer. A failure here leaves the server as it was.
awg-quick strip "$tmp/awg0.conf" >"$tmp/strip"
[[ -s "$tmp/strip" ]] || { echo "error: awg-quick strip gave an empty conf, nothing changed" >&2; exit 1; }

# mktemp makes both files itself (new, random names); cp -p then gives them
# the conf's owner and mode.
bak="$(mktemp "$CONF.bak-import-$(date -u +%Y%m%dT%H%M%SZ).XXXXXX")"
cp -p "$CONF" "$bak"
W="$(mktemp "$CONF.import.XXXXXX")"
cp -p "$tmp/awg0.conf" "$W" # same dir, so the mv is atomic
mv "$W" "$CONF"

awg syncconf awg0 "$tmp/strip"
