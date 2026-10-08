#!/usr/bin/env bash
# Prints the bot's environment for the VPN server:
#   - app settings from .env, minus local-only keys (test Telegram server,
#     dev docker wrapper, local Mongo settings);
#   - MONGO_URI for the geoirb_vpn_bot user on 127.0.0.1, with the password
#     from ../server-infra/secret/database.yaml — the source of truth for
#     Mongo users (server-infra/scripts/add-user.sh writes it).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$ROOT/.env}"
DB_SECRETS="${DB_SECRETS:-$ROOT/../server-infra/secret/database.yaml}"
SERVER="${SERVER:-geoirb-vpn}"
DB="geoirb_vpn"
DB_USER="geoirb_vpn_bot"

[[ -f "$DB_SECRETS" ]] || { echo "error: $DB_SECRETS not found" >&2; exit 1; }
password="$(awk -v srv="$SERVER:" -v db="    $DB:" -v user="      $DB_USER:" '
  $0 == srv { in_srv = 1; next }
  in_srv && /^[^ ]/ { exit }
  in_srv && $0 == db { in_db = 1; next }
  in_db && $0 == user { in_user = 1; next }
  in_user && /^ +password:/ { sub(/^ +password:[ ]*/, ""); print; exit }
' "$DB_SECRETS")"
[[ -n "$password" ]] || { echo "error: no $SERVER.mongo.$DB.$DB_USER password in $DB_SECRETS" >&2; exit 1; }

grep -vE '^[[:space:]]*(#|$)' "$ENV_FILE" |
  grep -vE '^(TELEGRAM_TEST_ENV|DOCKER_BIN|MONGO_URI|MONGO_DB|MONGO_USERNAME|MONGO_PASSWORD|BACKUP_STAMP|MAINTENANCE_FLAG)='
# The password goes to python through stdin, never argv (ps).
encoded="$(printf '%s' "$password" | python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.stdin.read(), safe=""))')"
echo "MONGO_URI=mongodb://$DB_USER:$encoded@127.0.0.1:27017/$DB?authSource=$DB"
# deploy/backup.sh touches it after every good backup; the bot alerts when it gets old.
echo "BACKUP_STAMP=/var/lib/geoirb-vpn-bot/last-backup"
# exists while the admin's "maintenance" is on (the bot's state directory)
echo "MAINTENANCE_FLAG=/var/lib/geoirb-vpn-bot/maintenance"
