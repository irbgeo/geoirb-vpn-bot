#!/usr/bin/env bash
# Usage: awg0-check.sh <conf>. Runs as root before every `awg-quick up/down`
# of awg0 (geoirb-awg0.service). The bot user owns that conf, and awg-quick
# runs PreUp/PostUp/PreDown/PostDown lines as root shell (SaveConfig rewrites
# the file as root): a conf with such a line is refused, so code running as
# the bot can not become root through it. awg-quick reads keys without case
# and with any indent, so the check does too.
set -euo pipefail
CONF="${1:?usage: awg0-check.sh <conf>}"
[[ -r "$CONF" ]] || { echo "error: $CONF is missing or unreadable" >&2; exit 1; }
if grep -Ein '^[[:space:]]*(PreUp|PostUp|PreDown|PostDown|SaveConfig)[[:space:]]*=' "$CONF" >&2; then
  echo "error: $CONF holds a hook or SaveConfig line (above); awg-quick would run it as root. Remove it." >&2
  exit 1
fi
