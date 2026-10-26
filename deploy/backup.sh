#!/usr/bin/env bash
# Runs on the VPN server as root (geoirb-vpn-bot-backup.timer, daily).
# Makes /var/backups/geoirb-vpn-bot/geoirb-vpn-<UTC time>.tar.gz with:
#   amnezia-awg.tar.gz          /etc/amnezia/amneziawg (awg0.conf: server key,
#                               obfuscation, client peers)
#   mongo-geoirb_vpn.archive.gz mongodump of the bot database
# and keeps the newest KEEP archives. DB_SECRET_KEY is NOT in the archive:
# keep it separately, or the encrypted client keys in the dump can't be read.
# With BACKUP_AGE_RECIPIENT (an age public key) in the env file the archive
# is encrypted here (.tar.gz.age): it holds the server's private key and
# every PSK, and only the holder of the age secret key can read it. Without
# it the archive stays plain and the run says so.
# ROOT is a path prefix for tests (deploy/backup_test.sh).
set -euo pipefail

DEST="${DEST:-/var/backups/geoirb-vpn-bot}"
KEEP="${KEEP:-7}"
ENV_FILE="${ENV_FILE:-/etc/geoirb-vpn-bot/env}"
MONGO_CONTAINER="${MONGO_CONTAINER:-server-infra-mongo-1}"
STAMP="$(grep -m1 '^BACKUP_STAMP=' "$ENV_FILE" | cut -d= -f2- || true)"
MONGO_URI="$(grep -m1 '^MONGO_URI=' "$ENV_FILE" | cut -d= -f2- || true)"
RECIPIENT="$(grep -m1 '^BACKUP_AGE_RECIPIENT=' "$ENV_FILE" | cut -d= -f2- || true)"
: "${MONGO_URI:?no MONGO_URI in $ENV_FILE}"

umask 077
mkdir -p "$DEST"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

tar -C "${ROOT:-}/etc/amnezia" -czf - amneziawg >"$tmp/amnezia-awg.tar.gz"
# The URI (with the password) goes in through a config file on stdin, so it
# never shows up in `ps`.
printf 'uri: %s\n' "$MONGO_URI" | docker exec -i "$MONGO_CONTAINER" sh -c \
  'f=$(mktemp); cat >"$f"; mongodump --quiet --config "$f" --archive --gzip; rc=$?; rm -f "$f"; exit $rc' \
  >"$tmp/mongo-geoirb_vpn.archive.gz"

# Write to .part and rename only when complete: a broken archive must never
# count as one of the $KEEP and push good ones out.
out="$DEST/geoirb-vpn-$(date -u +%Y%m%d-%H%M%S).tar.gz"
[[ -z "$RECIPIENT" ]] || out="$out.age"
trap 'rm -rf "$tmp" "$out.part"' EXIT
if [[ -n "$RECIPIENT" ]]; then
  tar -C "$tmp" -czf - amnezia-awg.tar.gz mongo-geoirb_vpn.archive.gz | age -r "$RECIPIENT" -o "$out.part"
else
  echo "backup: not encrypted (set BACKUP_AGE_RECIPIENT): it holds the server key and every PSK" >&2
  tar -C "$tmp" -czf "$out.part" amnezia-awg.tar.gz mongo-geoirb_vpn.archive.gz
fi
mv "$out.part" "$out"
echo "backup: $out ($(du -h "$out" | cut -f1))"

# keep the newest $KEEP, plain and encrypted together
# (one of the two patterns may match nothing: ls then fails, that is fine)
{ ls -1t "$DEST"/geoirb-vpn-*.tar.gz "$DEST"/geoirb-vpn-*.tar.gz.age 2>/dev/null || true; } |
  tail -n +"$((KEEP + 1))" | xargs -r rm --

# The bot reads this file's time and tells the admins when it gets old.
# A new file moved over the name, not `touch`: a symlink planted as the
# stamp is replaced, not followed. The stamp is in root's own directory
# (/var/lib/geoirb-vpn, scripts/server-env.sh); the habit is kept.
if [[ -n "$STAMP" ]]; then
  install -d "$(dirname "$STAMP")"
  s="$(mktemp "$STAMP.XXXXXX")"
  chmod 644 "$s" # mktemp makes it 600; the same mode as ru-nets.stamp
  mv -f "$s" "$STAMP"
fi
