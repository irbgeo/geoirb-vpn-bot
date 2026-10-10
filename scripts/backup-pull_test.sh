#!/usr/bin/env bash
# Dry test of scripts/backup-pull.sh with a fake ssh and a fake access file:
# no real server, and BACKUP_DEST keeps it out of ./backups.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$DIR/testlib.sh"

mkdir "$TMP/bin"
# ssh: the `ls` command prints the newest archive's path (nothing with
# $NO_BACKUP); `cat` prints it, or half of it and fails with $PULL_FAIL.
cat >"$TMP/bin/ssh" <<'X'
#!/usr/bin/env bash
cmd="${*: -1}"
case "$cmd" in
  *"ls -1t"*) [[ -n "${NO_BACKUP:-}" ]] || echo /var/backups/geoirb-vpn-bot/geoirb-vpn-20261010-040000.tar.gz ;;
  *"sudo cat"*) printf HALF; [[ -z "${PULL_FAIL:-}" ]] || exit 1; printf DONE ;;
esac
X
chmod +x "$TMP/bin"/*
printf 'host: ru.example\nuser: u\n' >"$TMP/access.yaml"
D="$TMP/backups"
run() { env PATH="$TMP/bin:$PATH" ACCESS_FILE="$TMP/access.yaml" BACKUP_DEST="$D" "$@" bash "$DIR/backup-pull.sh" >"$TMP/out" 2>&1; }
files() { ls "$D" | tr '\n' ' ' | sed 's/ $//'; }
NEW=geoirb-vpn-20261010-040000.tar.gz

run PULL_FAIL=1
check "failed pull fails" "1" "$([[ $? -ne 0 ]] && echo 1)"
check "failed pull: no archive with the final name" "0" "$(ls "$D" | grep -c '\.tar\.gz$')"

run NO_BACKUP=1
check "no backup on the server fails" "1" "$([[ $? -ne 0 ]] && echo 1)"

# Eight older archives here; 7 stay with the new one counted.
for d in 1 2 3 4 5 6 7 8; do
  echo old >"$D/geoirb-vpn-2020010$d-000000.tar.gz"
  touch -t "2020010${d}0000" "$D/geoirb-vpn-2020010$d-000000.tar.gz"
done
before="$(files)"
run PULL_FAIL=1
check "failed pull deletes nothing" "$before" "$(files)"
rm -f "$D"/*.part
run
check "exits 0" "0" "$?"
check "archive pulled whole" "HALFDONE" "$(cat "$D/$NEW")"
check "archive and directory are private" "600 700" "$(mode "$D/$NEW") $(mode "$D")"
check "the newest 7 stay, no .part" "geoirb-vpn-20200103-000000.tar.gz geoirb-vpn-20200104-000000.tar.gz geoirb-vpn-20200105-000000.tar.gz geoirb-vpn-20200106-000000.tar.gz geoirb-vpn-20200107-000000.tar.gz geoirb-vpn-20200108-000000.tar.gz $NEW" "$(files)"

finish
