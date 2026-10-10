#!/usr/bin/env bash
# Tests deploy/backup.sh with ROOT=<tmp> (the /etc/amnezia prefix), its DEST
# and ENV_FILE overrides and a fake docker on PATH.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$DIR/../scripts/testlib.sh"

mkdir "$TMP/bin"
# docker exec ... mongodump: logs argv and stdin (the config with the URI),
# prints a dump; with $DOCKER_FAIL it prints half a dump and fails.
cat >"$TMP/bin/docker" <<'X'
#!/usr/bin/env bash
echo "docker $*" >>"$CALLS"
cat >"$CALLS.stdin"
printf DUMP
[[ -z "${DOCKER_FAIL:-}" ]]
X
chmod +x "$TMP/bin"/*

R="$TMP/root"
D="$TMP/dest"
STAMP="$TMP/state/last-backup"
mkdir -p "$R/etc/amnezia/amneziawg"
echo "[Interface]" >"$R/etc/amnezia/amneziawg/awg0.conf"
printf 'BOT_TOKEN=1:a\nMONGO_URI=mongodb://u:s3cret@127.0.0.1/db?a=b\nBACKUP_STAMP=%s\n' "$STAMP" >"$TMP/env"
run() { # run [env...]
  : >"$TMP/calls"
  env CALLS="$TMP/calls" ROOT="$R" DEST="$D" ENV_FILE="$TMP/env" KEEP=2 PATH="$TMP/bin:$PATH" "$@" \
    bash "$DIR/backup.sh" >"$TMP/out" 2>&1
}
archives() { ls "$D" 2>/dev/null | tr '\n' ' ' | sed 's/ $//'; }

# Three older archives; the run keeps the newest KEEP=2 (its own and one old).
mkdir -p "$D"
for d in 01 02 03; do
  echo old >"$D/geoirb-vpn-202001$d-000000.tar.gz"
  touch -t "202001${d}0000" "$D/geoirb-vpn-202001$d-000000.tar.gz"
done
run
check "exits 0" "0" "$?"
new="$(ls -1t "$D" | head -1)"
check "archive name" "1" "$(grep -cE '^geoirb-vpn-[0-9]{8}-[0-9]{6}\.tar\.gz$' <<<"$new")"
check "archive holds the conf and the dump" "amnezia-awg.tar.gz mongo-geoirb_vpn.archive.gz" "$(tar -tzf "$D/$new" | tr '\n' ' ' | sed 's/ $//')"
mkdir "$TMP/x"
tar -C "$TMP/x" -xzf "$D/$new"
check "the conf is in it" "1" "$(tar -tzf "$TMP/x/amnezia-awg.tar.gz" | grep -c '^amneziawg/awg0.conf$')"
check "the dump is in it" "DUMP" "$(cat "$TMP/x/mongo-geoirb_vpn.archive.gz")"
check "archive is root only" "600" "$(mode "$D/$new")"
check "the newest KEEP archives stay, older ones go" "geoirb-vpn-20200103-000000.tar.gz $new" "$(archives)"
check "URI reaches mongodump on stdin, never argv" "uri: mongodb://u:s3cret@127.0.0.1/db?a=b|0" "$(cat "$TMP/calls.stdin")|$(grep -c s3cret "$TMP/calls")"
check "stamp touched" "1" "$([[ -f "$STAMP" ]] && echo 1)"

# A failed dump: no archive, no .part, nothing deleted, the stamp stays old.
touch -t 200001010000 "$STAMP"
before="$(archives)"
run DOCKER_FAIL=1
check "failed dump fails" "1" "$([[ $? -ne 0 ]] && echo 1)"
check "failed dump: no new archive, no .part, old ones kept" "$before" "$(archives)"
check "failed dump: stamp not touched" "1" "$([[ "$STAMP" -ot "$TMP/env" ]] && echo 1)"

# The bot user owns the stamp's directory: a symlink planted as the stamp is
# replaced, root never writes through it.
echo keep >"$TMP/victim"
touch -t 200001010000 "$TMP/victim"
ln -sf "$TMP/victim" "$STAMP"
run
check "planted stamp symlink: replaced by a file, target untouched" "0 1 1 keep" \
  "$? $([[ -f "$STAMP" && ! -L "$STAMP" ]] && echo 1) $([[ "$TMP/victim" -ot "$TMP/env" ]] && echo 1) $(cat "$TMP/victim")"
check "no temp stamp left" "last-backup" "$(ls "$(dirname "$STAMP")")"
before="$(archives)"

grep -v '^MONGO_URI=' "$TMP/env" >"$TMP/env.nouri"
run ENV_FILE="$TMP/env.nouri"
check "no MONGO_URI fails" "1" "$([[ $? -ne 0 ]] && echo 1)"
check "no MONGO_URI: nothing run, nothing deleted" "0 $before" "$(grep -c . "$TMP/calls") $(archives)"

finish
