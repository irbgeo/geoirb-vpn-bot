#!/usr/bin/env bash
# Tests scripts/lib.sh `remote` with fake ssh/sshpass on PATH: the ssh key is
# tried first, a password in the access file is only the fallback, and a file
# without a password means key-only (no prompt, no sshpass). No real server.
set -uo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
FAILS=0
check() { # check <name> <expected> <actual>
  if [[ "$2" == "$3" ]]; then echo "ok   $1"; else
    echo "FAIL $1"; echo "     expected: $2"; echo "     actual:   $3"; FAILS=$((FAILS + 1)); fi
}

mkdir "$TMP/bin"
# Both fakes log their argv; sshpass also logs the password it got in the env.
printf '#!/usr/bin/env bash\necho "ssh $*" >>"$CALLS"\n' >"$TMP/bin/ssh"
printf '#!/usr/bin/env bash\necho "sshpass $* | SSHPASS=${SSHPASS-<unset>}" >>"$CALLS"\n' >"$TMP/bin/sshpass"
chmod +x "$TMP/bin"/*
printf 'geoirb:\n  host: 10.0.0.1\n  port: 2222\n  user: root\n  password: s3cret\n' >"$TMP/pw.yaml"
printf 'geoirb:\n  host: 10.0.0.2\n  user: admin\n  # password: old-one\n' >"$TMP/key.yaml"

# run <access file> — sources lib.sh and runs one remote command.
run() {
  : >"$TMP/calls"
  env -u SSHPASS CALLS="$TMP/calls" PATH="$TMP/bin:$PATH" ACCESS_FILE="$1" \
    bash -c 'source "$0/lib.sh" && remote uptime -p && echo "leaked=${SSHPASS-<unset>}"' "$DIR" 2>&1
}

out="$(run "$TMP/pw.yaml")"
check "password in the file: sshpass, the password only in its env" \
  "sshpass -e ssh -o ConnectTimeout=15 -p 2222 root@10.0.0.1 uptime -p | SSHPASS=s3cret" "$(cat "$TMP/calls")"
check "the key is tried first (pubkey auth is not switched off)" "0" "$(grep -c 'PubkeyAuthentication=no' "$TMP/calls")"
check "the password is not exported to other commands" "leaked=<unset>" "$out"

out="$(run "$TMP/key.yaml")"
check "no password in the file: key only, never a prompt, default port" \
  "ssh -o BatchMode=yes -o ConnectTimeout=15 -p 22 admin@10.0.0.2 uptime -p" "$(cat "$TMP/calls")"
check "a commented password is not used" "0" "$(grep -c 'old-one' "$TMP/calls")"

echo
[[ "$FAILS" -eq 0 ]] && echo "all tests passed" || { echo "$FAILS failed"; exit 1; }
