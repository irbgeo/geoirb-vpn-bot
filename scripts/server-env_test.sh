#!/usr/bin/env bash
# Tests scripts/server-env.sh with a fake .env and database.yaml.
set -uo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
FAILS=0
check() { # check <name> <expected> <actual>
  if [[ "$2" == "$3" ]]; then echo "ok   $1"; else
    echo "FAIL $1"; echo "     expected: $2"; echo "     actual:   $3"; FAILS=$((FAILS + 1)); fi
}

cat >"$TMP/.env" <<'X'
BOT_TOKEN=123:abc
MONGO_USERNAME=old
MONGO_PASSWORD=wrong
MONGO_URI=mongodb://localhost:27017
DB_SECRET_KEY=a2V5
ENDPOINT_HOST=35.217.30.38
TELEGRAM_TEST_ENV=true
AWG_EXEC=scripts/dev-remote.sh
# a comment
TARIFFS=30:150,90:400
X
cat >"$TMP/database.yaml" <<'X'
geoirb-bots:
  mongo:
    geoirb_vpn:
      geoirb_vpn_bot:
        password: other-server
geoirb-vpn:
  host: 35.217.30.38
  mongo:
    port: 27017
    root:
      password: rootpw
    geoirb_vpn:
      geoirb_vpn_bot:
        permission: readWrite
        password: p@ss/w0rd
X

out="$(ENV_FILE="$TMP/.env" DB_SECRETS="$TMP/database.yaml" "$DIR/server-env.sh")"
check "keeps app settings" \
  "BOT_TOKEN=123:abc
DB_SECRET_KEY=a2V5
ENDPOINT_HOST=35.217.30.38
TARIFFS=30:150,90:400
MONGO_URI=mongodb://geoirb_vpn_bot:p%40ss%2Fw0rd@127.0.0.1:27017/geoirb_vpn?authSource=geoirb_vpn
BACKUP_STAMP=/var/lib/geoirb-vpn-bot/last-backup
MAINTENANCE_FLAG=/var/lib/geoirb-vpn-bot/maintenance" \
  "$out"

ENV_FILE="$TMP/.env" DB_SECRETS="$TMP/nope.yaml" "$DIR/server-env.sh" >/dev/null 2>&1
check "fails without the DB password" "1" "$?"

echo
[[ "$FAILS" -eq 0 ]] && echo "all tests passed" || { echo "$FAILS failed"; exit 1; }
