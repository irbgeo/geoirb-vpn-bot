#!/usr/bin/env bash
# Creates the AmneziaWG server conf /etc/amnezia/amneziawg/awg0.conf on the
# first install only: server key from `awg genkey`, random AWG 2.0
# obfuscation. An existing conf (server key, client peers) is never touched.
# Runs as root from deploy/install.sh; ROOT is a path prefix for tests.
set -euo pipefail
DIR="${ROOT:-}/etc/amnezia/amneziawg"
CONF="$DIR/awg0.conf"

# The bot (group vpnbot) rewrites the conf through .tmp/.bak files here.
install -d -m 770 "$DIR"
chown root:vpnbot "$DIR"
if [[ -e "$CONF" ]]; then
  echo "awg0.conf kept"
  exit 0
fi

rand() { echo $(($(od -An -N4 -tu4 /dev/urandom) % ($2 - $1 + 1) + $1)); } # modulo bias is irrelevant here
hex() { od -An -tx1 -N"$1" /dev/urandom | tr -d ' \n'; }

# AmneziaWG wants S1 + 56 != S2; with both in 15..60 that always holds.
s1="$(rand 15 60)"
s2="$(rand 15 60)"

# H1-H4: one range in each quarter of 5..2^32-1, so they never overlap.
# Ranges `a-b` are accepted by amneziawg-tools v3.1.20260812 (src/type.c).
q=1073741822
hs=()
for i in 0 1 2 3; do
  lo=$((5 + i * q + $(rand 0 $((q / 2)))))
  hs+=("$lo-$((lo + $(rand 1000 $((q / 2 - 1)))))")
done

# I1: 64-128 bytes starting like a QUIC v1 Initial (long-header byte
# 0xc0-0xc3: Initial type, version 1, 8-byte DCID), random bytes after. Trade-off: a real
# Initial is >= 1200 bytes, but that makes client configs too long for a QR
# code. Commented: the server does not send it, the bot un-comments it for clients.
i1="c$(rand 0 3)00000001 08$(hex 8)$(hex "$(rand 50 114)")"
key="$(awg genkey)"

umask 077
{
  echo "[Interface]"
  echo "Address = 10.8.0.1/22"
  echo "ListenPort = 443"
  echo "MTU = 1380"
  echo "PrivateKey = $key"
  echo "Jc = $(rand 4 8)"
  echo "Jmin = 40"
  echo "Jmax = 70"
  echo "S1 = $s1"
  echo "S2 = $s2"
  echo "S3 = $(rand 8 32)"
  echo "S4 = $(rand 8 32)"
  for i in 0 1 2 3; do echo "H$((i + 1)) = ${hs[$i]}"; done
  echo "# I1 = <b 0x${i1// /}>"
} >"$CONF.tmp"
mv "$CONF.tmp" "$CONF"
chown vpnbot:vpnbot "$CONF"
echo "awg0.conf created"
