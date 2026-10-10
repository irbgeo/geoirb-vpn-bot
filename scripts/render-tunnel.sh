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
# An empty file would make awk read the template as the key file and print
# an empty conf with exit code 0.
[[ -s "$FILE" ]] || { echo "error: $FILE is missing or empty" >&2; exit 1; }

# One awk run: the keys are read from the file and the host from the
# environment, so no secret is ever in argv (ps). Replacements are literal
# (no sed/gsub specials); the host goes last so it is copied as is.
EXIT_HOST="$host" awk -v file="$FILE" '
  function put(line, from, to,    i, out) {
    out = ""
    while ((i = index(line, from)) > 0) {
      out = out substr(line, 1, i - 1) to
      line = substr(line, i + length(from))
    }
    return out line
  }
  BEGIN { n = split("ru_private ru_public exit_private exit_public psk port jc jmin jmax s1 s2 h1 h2 h3 h4", keys, " ") }
  NR == FNR {
    k = $1; sub(/:$/, "", k)
    if (k ":" == $1 && !(k in val)) { v = $0; sub(/^[^:]*:[[:space:]]*/, "", v); val[k] = v }
    next
  }
  FNR == 1 {
    for (j = 1; j <= n; j++) if (val[keys[j]] == "") { print "missing " keys[j] " in " file > "/dev/stderr"; bad = 1; exit 1 }
  }
  {
    for (j = 1; j <= n; j++) $0 = put($0, "@" toupper(keys[j]) "@", val[keys[j]])
    print put($0, "@EXIT_HOST@", ENVIRON["EXIT_HOST"])
  }
  END { if (bad) exit 1 }
' "$FILE" "$tmpl"
