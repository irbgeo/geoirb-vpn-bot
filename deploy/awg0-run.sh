#!/usr/bin/env bash
# Usage: awg0-run.sh up|down <conf>. Runs as root for geoirb-awg0.service.
# The conf belongs to the bot user, and awg-quick runs its hook lines as
# root. Checking the bot's file and then letting awg-quick read it again
# leaves a gap: the bot could swap the file in between. So root takes its
# own copy (in a root-only directory, named like the conf so the interface
# name stays), checks that copy (awg0-check.sh) and runs awg-quick on it.
# RUN_DIR, AWG_QUICK and CHECK can be changed for tests.
set -euo pipefail
act="${1:-}"
conf="${2:-}"
if [[ ("$act" != up && "$act" != down) || -z "$conf" ]]; then
  echo "usage: $0 up|down <conf>" >&2
  exit 1
fi
RUN_DIR="${RUN_DIR:-/run/geoirb-awg0}"
AWG_QUICK="${AWG_QUICK:-/usr/bin/awg-quick}"
CHECK="${CHECK:-$(dirname "${BASH_SOURCE[0]}")/awg0-check.sh}"

# a symlink could point root at any file
[[ -f "$conf" && ! -L "$conf" ]] || { echo "error: $conf is not a plain file" >&2; exit 1; }
install -d -m 700 "$RUN_DIR"
copy="$RUN_DIR/$(basename "$conf")"
install -m 600 "$conf" "$copy.new"
mv -f "$copy.new" "$copy"
bash "$CHECK" "$copy"
exec "$AWG_QUICK" "$act" "$copy"
