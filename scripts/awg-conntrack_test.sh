#!/usr/bin/env bash
# Tests deploy/awg-conntrack.sh with a fake docker on PATH.
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
# fake docker: `ps` lists the container, `exec … cat` prints $TMP/inside,
# `exec … sh -c "echo N >…"` writes N there and logs the call.
cat >"$TMP/bin/docker" <<X
#!/usr/bin/env bash
case "\$1" in
  ps) echo amnezia-awg2 ;;
  exec)
    if [[ "\$3" == cat ]]; then cat "$TMP/inside"; else
      echo "\$5" | sed -E 's/^echo ([0-9]+) .*/\1/' >"$TMP/inside"; echo write >>"$TMP/calls"; fi ;;
esac
X
chmod +x "$TMP/bin/docker"
echo 7200 >"$TMP/host"
: >"$TMP/env"
run() { PATH="$TMP/bin:$PATH" ENV_FILE="$TMP/env" HOST_PROC_KEY="$TMP/host" "$DIR/../deploy/awg-conntrack.sh"; }

echo 432000 >"$TMP/inside"
run >/dev/null
check "copies the host value in" "7200" "$(cat "$TMP/inside")"
check "one write" "1" "$(wc -l <"$TMP/calls" | tr -d ' ')"

run >/dev/null
check "no write when already right" "1" "$(wc -l <"$TMP/calls" | tr -d ' ')"

echo
[[ "$FAILS" -eq 0 ]] && echo "all tests passed" || { echo "$FAILS failed"; exit 1; }
