#!/usr/bin/env bash
# Usage: import-peers.sh <old.conf>. Runs as root on the RU server.
# Appends the [Peer] blocks of the old server's conf to awg0.conf, verbatim,
# when their PublicKey is not there yet and every AllowedIPs address is inside
# the Address subnet of awg0.conf; then applies them live (no client drops).
# ROOT is a path prefix for tests.
set -euo pipefail
OLD="${1:?usage: import-peers.sh <old.conf>}"
CONF="${ROOT:-}/etc/amnezia/amneziawg/awg0.conf"
TMP="$CONF.tmp"
trap 'rm -f "$TMP" "$TMP.new" "$TMP.skip"' EXIT

# Pass 1 reads the new conf (keys, subnet), pass 2 the old one. Blocks to
# import go to stdout; skipped keys (prefix only, never the PSK) to stderr.
awk -v new="$CONF" -v skipfile="$TMP.skip" '
function ip2n(s,  a) { split(s, a, "."); return ((a[1] * 256 + a[2]) * 256 + a[3]) * 256 + a[4] }
function inside(c,  a, ip) {
  split(c, a, "/"); ip = ip2n(a[1])
  return ip >= base && ip < base + size
}
function flush(  i, ok, n, ips) {
  if (sect != "Peer") { blk = ""; return }
  ok = (key != "")
  n = split(ips_s, ips, ",")
  for (i = 1; i <= n; i++) { gsub(/[ \t]/, "", ips[i]); if (ips[i] != "" && !inside(ips[i])) ok = 0 }
  if (key in seen) { skipped++; skiplist = skiplist " " substr(key, 1, 8) "(dup)" }
  else if (!ok) { skipped++; skiplist = skiplist " " substr(key, 1, 8) "(subnet)" }
  else { imported++; seen[key] = 1; printf "\n%s", blk }
  blk = ""
}
BEGIN {
  while ((getline l < new) > 0) {
    if (l ~ /^PublicKey[ \t]*=/) { sub(/^[^=]*=[ \t]*/, "", l); seen[l] = 1 }
    if (l ~ /^Address[ \t]*=/) { sub(/^[^=]*=[ \t]*/, "", l); split(l, a, "/");
      size = 2 ^ (32 - a[2]); base = int(ip2n(a[1]) / size) * size }
  }
  if (size == 0) { print "error: no Address in " new > "/dev/stderr"; exit 1 }
}
/^\[/ { flush(); sect = substr($0, 2, index($0, "]") - 2); key = ""; ips_s = "" }
sect == "Peer" {
  if ($0 ~ /^PublicKey[ \t]*=/) { key = $0; sub(/^[^=]*=[ \t]*/, "", key); gsub(/[ \t\r]+$/, "", key) }
  if ($0 ~ /^AllowedIPs[ \t]*=/) { v = $0; sub(/^[^=]*=[ \t]*/, "", v); ips_s = ips_s "," v }
  if ($0 !~ /^[ \t\r]*$/) blk = blk $0 "\n"
}
END { flush(); print "imported " imported + 0 ", skipped " skipped + 0 (skiplist != "" ? " (" substr(skiplist, 2) ")" : "") > skipfile }
' "$OLD" >"$TMP.new"

cp -p "$CONF" "$CONF.bak"
cp -p "$CONF" "$TMP" # same dir, same owner and mode
[[ -z "$(tail -c1 "$CONF")" ]] || echo >>"$TMP"
cat "$TMP.new" >>"$TMP"
mv "$TMP" "$CONF"

awg syncconf awg0 <(awg-quick strip "$CONF")
cat "$TMP.skip"
rm -f "$TMP.skip"
