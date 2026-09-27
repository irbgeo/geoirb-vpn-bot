#!/usr/bin/env bash
# Shared helpers for scripts that talk to the VPN server described by the
# first block of secret/server-access.yaml (host, port, user, password).
# The password goes to sshpass via the SSHPASS env var, never argv.

LIB_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ACCESS_FILE="${ACCESS_FILE:-$LIB_ROOT/secret/server-access.yaml}"

# access_value <key> — first value of <key> in the access file.
access_value() {
  awk -v k="$1" '$1 == k":" { sub(/^[^:]*:[[:space:]]*/, ""); print; exit }' "$ACCESS_FILE"
}

SERVER_HOST="$(access_value host)"
SERVER_PORT="$(access_value port)"
SERVER_PORT="${SERVER_PORT:-22}"
SERVER_USER="$(access_value user)"
export SSHPASS="$(access_value password)"

# remote <cmd...> — run a command on the server.
remote() {
  sshpass -e ssh -o PubkeyAuthentication=no -o ConnectTimeout=15 -p "$SERVER_PORT" \
    "$SERVER_USER@$SERVER_HOST" "$@"
}

# upload <local> <remote path> — copy a file to the server.
upload() {
  sshpass -e scp -o PubkeyAuthentication=no -o ConnectTimeout=15 -P "$SERVER_PORT" \
    "$1" "$SERVER_USER@$SERVER_HOST:$2"
}
