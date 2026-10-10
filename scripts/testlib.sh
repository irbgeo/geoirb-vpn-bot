#!/usr/bin/env bash
# Shared helpers of the shell tests (scripts/*_test.sh, deploy/*_test.sh,
# deploy/exit/*_test.sh). A test sources it first: it gets $TMP (a temp dir,
# removed on exit), `check`, `mode`, and ends with `finish`.
set -uo pipefail
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
FAILS=0

# check <name> <expected> <actual>
check() {
  if [[ "$2" == "$3" ]]; then echo "ok   $1"; else
    echo "FAIL $1"; echo "     expected: $2"; echo "     actual:   $3"; FAILS=$((FAILS + 1)); fi
}

# mode <file> — its permission bits (GNU and BSD stat).
mode() { stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1"; }

# finish — the last line of a test: the summary; exit code 1 when a check failed.
finish() {
  echo
  if [[ "$FAILS" -eq 0 ]]; then echo "all tests passed"; else echo "$FAILS failed"; exit 1; fi
}
