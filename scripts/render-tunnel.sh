#!/usr/bin/env bash
# render-tunnel.sh <ru|exit> <exit_host> — prints one side's AmneziaWG conf from
# secret/tunnel.yaml (TUNNEL_FILE overrides). Each side gets only its own private key.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FILE="${TUNNEL_FILE:-$ROOT/secret/tunnel.yaml}"
side="${1:-}"
host="${2:-}"
case "$side" in
  ru) tmpl="$ROOT/deploy/awg-exit.conf.tmpl" ;;
  exit) tmpl="$ROOT/deploy/exit/awg-exit.conf.tmpl" ;;
  *) echo "usage: $0 <ru|exit> <exit_host>" >&2; exit 1 ;;
esac
[[ -n "$host" ]] || { echo "usage: $0 <ru|exit> <exit_host>" >&2; exit 1; }

val() { awk -v k="$1" '$1 == k":" { sub(/^[^:]*:[[:space:]]*/, ""); print; exit }' "$FILE"; }

args=(-e "s|@EXIT_HOST@|$host|g")
for k in ru_private ru_public exit_private exit_public psk port jc jmin jmax s1 s2 h1 h2 h3 h4; do
  v="$(val "$k")"
  [[ -n "$v" ]] || { echo "missing $k in $FILE" >&2; exit 1; }
  # base64 has no '|' or '&' but may contain '/' — '|' is the sed delimiter.
  args+=(-e "s|@$(tr a-z A-Z <<<"$k")@|${v//&/\\&}|g")
done
sed "${args[@]}" "$tmpl"
