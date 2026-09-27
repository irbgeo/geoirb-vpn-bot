#!/usr/bin/env bash
# Runs as root on the VPN server (geoirb-vpn-conntrack.timer, every 5 min).
# The Amnezia container does its own NAT in its own network namespace, and
# a namespace starts with the kernel default TCP conntrack timeout (5 days),
# not the host's 99-geoirb-vpn.conf. So the host value is copied in; the
# timer brings it back after the container restarts. Changes nothing when
# the value is already right.
set -euo pipefail
. "$(dirname "$0")/awg-container.sh"

KEY=/proc/sys/net/netfilter/nf_conntrack_tcp_timeout_established
want="$(cat "${HOST_PROC_KEY:-$KEY}")"
have="$(docker exec "$AWG_CONTAINER" cat "$KEY")"
if [[ "$have" != "$want" ]]; then
  docker exec "$AWG_CONTAINER" sh -c "echo $want >$KEY"
  echo "conntrack: $AWG_CONTAINER tcp established timeout $have -> $want"
fi
