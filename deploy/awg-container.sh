# Sourced by backup.sh and awg-conntrack.sh (as root on the VPN server).
# Sets AWG_CONTAINER: AWG_CONTAINER from the bot's env file, else the
# running amnezia-awg2 / amnezia-awg, in the same order as the bot.
ENV_FILE="${ENV_FILE:-/etc/geoirb-vpn-bot/env}"
AWG_CONTAINER="$(grep -m1 '^AWG_CONTAINER=' "$ENV_FILE" | cut -d= -f2- || true)"
if [[ -z "$AWG_CONTAINER" ]]; then
  running="$(docker ps --format '{{.Names}}')"
  for name in amnezia-awg2 amnezia-awg; do
    if grep -qx "$name" <<<"$running"; then AWG_CONTAINER="$name"; break; fi
  done
fi
: "${AWG_CONTAINER:?no amnezia-awg2 or amnezia-awg container is running}"
