#!/usr/bin/env bash
# Dry test of scripts/import-peers.sh with a fake sshpass: no real server.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$DIR/testlib.sh"
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
mkdir "$TMP/t"
out="$(TMPDIR=$TMP/t CALLS="$TMP/calls" PATH="$TMP/bin:$PATH" OLD_ACCESS_FILE="$TMP/old.yaml" ACCESS_FILE="$TMP/new.yaml" bash "$DIR/import-peers.sh" 2>&1)"
check "exit 0" "0" "$?"
check "one session per server" "u@old.example
u@new.example" "$(cat "$TMP/calls")"
check "conf reaches RU on stdin" "1" "$(grep -c SECRETPSK "$TMP/calls.stdin")"
check "PSK not in argv" "0" "$(grep -c SECRETPSK "$TMP/calls.argv")"
check "PSK not printed" "0" "$(grep -c SECRETPSK <<<"$out")"
check "local temp dir removed" "0" "$(ls -A "$TMP/t" | wc -l | tr -d ' ')"
finish
