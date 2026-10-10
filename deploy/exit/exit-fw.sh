#!/usr/bin/env bash
# Usage: exit-fw.sh up|down <interface>. Firewall of the exit server for the
# tunnel; awg-exit.conf's PostUp/PostDown call it as root (installed as
# /usr/local/sbin/geoirb-exit-fw.sh). Forwarding on, NAT only for the RU
# server's tunnel address, the tunnel may not reach private or link-local
# networks (second guard), TCP MSS clamped.
# Safe to repeat: `down` removes every copy of each rule and goes on when one
# is already gone; `up` does the same first, so each rule is there once and
# the DROP rules sit above the ACCEPT, however the last run ended.
set -euo pipefail
act="${1:-}"
dev="${2:-}"
if [[ ("$act" != up && "$act" != down) || -z "$dev" ]]; then
  echo "usage: $0 up|down <interface>" >&2
  exit 1
fi

# rule <A|I> <table> <chain> <rule...>
rule() {
  local how="$1" tbl="$2"
  shift 2
  while iptables -t "$tbl" -C "$@" 2>/dev/null; do iptables -t "$tbl" -D "$@"; done
  [[ "$act" == down ]] || iptables -t "$tbl" "-$how" "$@"
}

wan="$(ip -4 route show default | awk '{print $5; exit}')"
if [[ "$act" == up ]]; then
  [[ -n "$wan" ]] || { echo "error: no default route, NAT can not be set" >&2; exit 1; }
  sysctl -w net.ipv4.ip_forward=1
fi
if [[ -n "$wan" ]]; then
  rule A nat POSTROUTING -s 10.255.255.1 -o "$wan" -j MASQUERADE
else
  echo "warning: no default route, the NAT rule is left as it is" >&2
fi
for net in 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 169.254.0.0/16 100.64.0.0/10; do
  rule I filter FORWARD -i "$dev" -d "$net" -j DROP
done
rule A filter FORWARD -i "$dev" -j ACCEPT
rule A filter FORWARD -o "$dev" -m state --state RELATED,ESTABLISHED -j ACCEPT
rule A mangle FORWARD -o "$dev" -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu
