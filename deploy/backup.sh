#!/usr/bin/env bash
# Runs on the VPN server as root (geoirb-vpn-bot-backup.timer, daily).
# Makes /var/backups/geoirb-vpn-bot/geoirb-vpn-<UTC time>.tar.gz with:
#   amnezia-awg.tar.gz          /opt/amnezia/awg from the Amnezia container
#                               (awg0.conf, server keys)
#   mongo-geoirb_vpn.archive.gz mongodump of the bot database
# and keeps the newest KEEP archives. DB_SECRET_KEY is NOT in the archive:
# keep it separately, or the encrypted client keys in the dump can't be read.
set -euo pipefail

DEST="${DEST:-/var/backups/geoirb-vpn-bot}"
KEEP="${KEEP:-7}"
ENV_FILE="${ENV_FILE:-/etc/geoirb-vpn-bot/env}"
MONGO_CONTAINER="${MONGO_CONTAINER:-server-infra-mongo-1}"
STAMP="$(grep -m1 '^BACKUP_STAMP=' "$ENV_FILE" | cut -d= -f2- || true)"
. "$(dirname "$0")/awg-container.sh"
MONGO_URI="$(grep -m1 '^MONGO_URI=' "$ENV_FILE" | cut -d= -f2- || true)"
: "${MONGO_URI:?no MONGO_URI in $ENV_FILE}"

umask 077
mkdir -p "$DEST"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

docker exec "$AWG_CONTAINER" tar -C /opt/amnezia -czf - awg >"$tmp/amnezia-awg.tar.gz"
# The URI (with the password) goes in through a config file on stdin, so it
# never shows up in `ps`.
printf 'uri: %s\n' "$MONGO_URI" | docker exec -i "$MONGO_CONTAINER" sh -c \
  'f=$(mktemp); cat >"$f"; mongodump --quiet --config "$f" --archive --gzip; rc=$?; rm -f "$f"; exit $rc' \
  >"$tmp/mongo-geoirb_vpn.archive.gz"

# Write to .part and rename only when complete: a broken archive must never
# count as one of the $KEEP and push good ones out.
out="$DEST/geoirb-vpn-$(date -u +%Y%m%d-%H%M%S).tar.gz"
trap 'rm -rf "$tmp" "$out.part"' EXIT
tar -C "$tmp" -czf "$out.part" amnezia-awg.tar.gz mongo-geoirb_vpn.archive.gz
mv "$out.part" "$out"
echo "backup: $out ($(du -h "$out" | cut -f1))"

# keep the newest $KEEP
ls -1t "$DEST"/geoirb-vpn-*.tar.gz | tail -n +"$((KEEP + 1))" | xargs -r rm --

# The bot reads this file's time and tells the admins when it gets old.
if [[ -n "$STAMP" ]]; then
  install -d "$(dirname "$STAMP")"
  touch "$STAMP"
fi
