#!/usr/bin/env bash
# Dry test of scripts/import-peers.sh with a fake sshpass: no real server.
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
for n in old new; do printf 'host: %s.example\nuser: u\npassword: secret-pw\n' "$n" >"$TMP/$n.yaml"; done
# fake sshpass: logs host and argv; the old host prints a conf, the RU host eats stdin.
cat >"$TMP/bin/sshpass" <<'F'
#!/usr/bin/env bash
host="${*: -2:1}"
echo "$host" >>"$CALLS"
echo "ARGV $*" >>"$CALLS.argv"
case "$host" in
  u@old.example) printf '[Interface]\nPrivateKey = K\n\n[Peer]\nPublicKey = A=\nPresharedKey = SECRETPSK\n' ;;
  *) cat >"$CALLS.stdin" ;;
esac
F
chmod +x "$TMP/bin/sshpass"
out="$(CALLS="$TMP/calls" PATH="$TMP/bin:$PATH" OLD_ACCESS_FILE="$TMP/old.yaml" ACCESS_FILE="$TMP/new.yaml" bash "$DIR/import-peers.sh" 2>&1)"
check "exit 0" "0" "$?"
check "one session per server" "u@old.example
u@new.example" "$(cat "$TMP/calls")"
check "conf reaches RU on stdin" "1" "$(grep -c SECRETPSK "$TMP/calls.stdin")"
check "PSK not in argv" "0" "$(grep -c SECRETPSK "$TMP/calls.argv")"
check "PSK not printed" "0" "$(grep -c SECRETPSK <<<"$out")"
echo
[[ "$FAILS" -eq 0 ]] && echo "all tests passed" || { echo "$FAILS failed"; exit 1; }
