#!/usr/bin/env bash
# Tests deploy/awg0-run.sh with a fake awg-quick and a temp RUN_DIR.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$DIR/../scripts/testlib.sh"

mkdir "$TMP/bin" "$TMP/etc"
# The fake awg-quick logs its args and the file it got, as it read it.
printf '#!/usr/bin/env bash\necho "awg-quick $*" >>"$CALLS"\ncat "$2" >>"$CALLS"\n' >"$TMP/bin/awg-quick"
chmod +x "$TMP/bin/awg-quick"
CONF="$TMP/etc/awg0.conf"
RUN="$TMP/run"

run() { # run <up|down> <conf>
  : >"$TMP/calls"
  CALLS="$TMP/calls" RUN_DIR="$RUN" AWG_QUICK="$TMP/bin/awg-quick" bash "$DIR/awg0-run.sh" "$1" "$2" >/dev/null 2>&1
}

printf '[Interface]\nPrivateKey = K\nListenPort = 443\n' >"$CONF"
run up "$CONF"
check "clean conf: exits 0" "0" "$?"
check "awg-quick runs the root copy, named awg0.conf" "awg-quick up $RUN/awg0.conf" "$(head -1 "$TMP/calls")"
check "the copy has the conf's content" "$(cat "$CONF")" "$(tail -n +2 "$TMP/calls")"
check "the copy is 600, its dir 700" "600 700" "$(mode "$RUN/awg0.conf") $(mode "$RUN")"

run down "$CONF"
check "down runs on the copy too" "awg-quick down $RUN/awg0.conf" "$(head -1 "$TMP/calls")"

printf '[Interface]\nPrivateKey = K\n  postup = id >/tmp/x\n' >"$CONF"
run up "$CONF"
check "a hook line: refused" "1" "$([[ $? -ne 0 ]] && echo 1)"
check "a hook line: awg-quick not run" "" "$(cat "$TMP/calls")"

printf '[Interface]\nPrivateKey = K\n' >"$TMP/etc/real.conf"
rm "$CONF"
ln -s "$TMP/etc/real.conf" "$CONF"
run up "$CONF"
check "a symlink conf: refused" "1" "$([[ $? -ne 0 ]] && echo 1)"
check "a symlink conf: awg-quick not run" "" "$(cat "$TMP/calls")"

run sideways "$CONF"
check "a bad action: refused" "1" "$([[ $? -ne 0 ]] && echo 1)"

U="$DIR/geoirb-awg0.service"
check "unit: every awg-quick goes through the wrapper" "0" "$(grep -c '/usr/bin/awg-quick' "$U")"
check "unit: start and stop use the wrapper" "ExecStartPre=-/opt/geoirb-vpn-bot/awg0-run.sh down /etc/amnezia/amneziawg/awg0.conf
ExecStart=/opt/geoirb-vpn-bot/awg0-run.sh up /etc/amnezia/amneziawg/awg0.conf
ExecStop=/opt/geoirb-vpn-bot/awg0-run.sh down /etc/amnezia/amneziawg/awg0.conf" "$(grep -E '^Exec' "$U")"

finish
