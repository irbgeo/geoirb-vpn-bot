#!/usr/bin/env bash
# Shared helpers for scripts that talk to the VPN server described by the
# first block of secret/server-access.yaml (host, port, user, optional
# password). ssh tries the key first (~/.ssh/id_*, the agent); a password in
# the file is only the fallback and goes to sshpass via the SSHPASS env var,
# never argv. No password in the file = key only.

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
SERVER_PASSWORD="$(access_value password)" # not exported: only `remote` hands it to sshpass

# remote <cmd...> — run a command on the server.
remote() {
  if [[ -n "$SERVER_PASSWORD" ]]; then
    SSHPASS="$SERVER_PASSWORD" sshpass -e ssh -o ConnectTimeout=15 -p "$SERVER_PORT" \
      "$SERVER_USER@$SERVER_HOST" "$@"
  else
    # BatchMode: fail at once instead of asking for a password nobody types
    ssh -o BatchMode=yes -o ConnectTimeout=15 -p "$SERVER_PORT" \
      "$SERVER_USER@$SERVER_HOST" "$@"
  fi
}

