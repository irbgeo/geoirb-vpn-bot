#!/usr/bin/env bash
# Copies the newest server backup to ./backups/ on this machine, so a lost
# VPS doesn't take its backups with it. Keeps the newest 7 here too.
# BACKUP_DEST overrides the directory (scripts/backup-pull_test.sh).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib.sh"
DEST="${BACKUP_DEST:-$ROOT/backups}"
KEEP=7

name="$(remote "sudo sh -c 'ls -1t /var/backups/geoirb-vpn-bot/geoirb-vpn-*.tar.gz | head -1'")"
[[ -n "$name" ]] || { echo "error: no backup on the server" >&2; exit 1; }
mkdir -p "$DEST"
chmod 700 "$DEST"
out="$DEST/$(basename "$name")"
# The archive is root-only (600) on the server: read it through sudo.
(umask 077 && remote "sudo cat '$name'" >"$out.part")
mv "$out.part" "$out"
echo "pulled: $out ($(du -h "$out" | cut -f1))"
ls -1t "$DEST"/geoirb-vpn-*.tar.gz | tail -n +"$((KEEP + 1))" | xargs -r rm --
