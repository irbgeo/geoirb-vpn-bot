#!/usr/bin/env bash
# Usage: import-peers.sh <old.conf>. Runs as root on the RU server.
# Appends the [Peer] blocks of the old server's conf to awg0.conf, verbatim,
# when their PublicKey is not there yet and AllowedIPs is one IPv4 /32 inside
# the Address subnet of awg0.conf and not used by another peer; then applies
# them live (no client drops).
# The running bot also rewrites awg0.conf (its sha check cannot see this
# script), so the bot is stopped first and started again on exit, if it ran.
# ROOT is a path prefix for tests.
set -euo pipefail
umask 077
OLD="${1:?usage: import-peers.sh <old.conf>}"
CONF="${ROOT:-}/etc/amnezia/amneziawg/awg0.conf"
W="$CONF.import" # own temp names, distinct from the bot's .tmp/.bak
BOT=geoirb-vpn-bot
restart=0
cleanup() {
  rm -f "$W.tmp" "$W.new" "$W.skip"
  [[ "$restart" == 0 ]] || systemctl start "$BOT"
}
trap cleanup EXIT

if systemctl is-active --quiet "$BOT"; then
  systemctl stop "$BOT"
  restart=1
fi

# Pass 1 reads the new conf (keys, taken IPs, subnet), pass 2 the old one.
# Blocks to import go to $W.new; the summary (skipped keys by 8-char prefix,
# never the PSK) to $W.skip.
awk -v new="$CONF" -v skipfile="$W.skip" '
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
  else if (ip2n(ip) < base || ip2n(ip) >= base + size) skip("subnet")
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
      size = 2 ^ (32 - a[2]); base = int(ip2n(a[1]) / size) * size }
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
' "$OLD" >"$W.new"

summary="$(cat "$W.skip")"
echo "$summary"
n="${summary#imported }"
n="${n%%,*}"
[[ "$n" != 0 ]] || exit 0

cp -p "$CONF" "$CONF.bak-import-$(date -u +%Y%m%dT%H%M%SZ)"
cp -p "$CONF" "$W.tmp" # same dir, same owner and mode
[[ -z "$(tail -c1 "$CONF")" ]] || echo >>"$W.tmp"
cat "$W.new" >>"$W.tmp"
mv "$W.tmp" "$CONF"

awg syncconf awg0 <(awg-quick strip "$CONF")
