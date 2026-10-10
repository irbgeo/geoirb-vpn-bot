#!/usr/bin/env bash
# Tests deploy/exit/install.sh with ROOT=<tmp> and fake commands on PATH.
set -uo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
FAILS=0
check() { # check <name> <expected> <actual>
  if [[ "$2" == "$3" ]]; then echo "ok   $1"; else
    echo "FAIL $1"; echo "     expected: $2"; echo "     actual:   $3"; FAILS=$((FAILS + 1)); fi
}
mode() { stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1"; }

TAG="$(sed -n 's/^AWG_TOOLS_TAG="${AWG_TOOLS_TAG-\([^}]*\)}".*/\1/p' "$DIR/../awg-tools.sh")"

mkdir "$TMP/bin"
# Every fake logs its name and args; `awg --version` prints $AWG_VERSION.
for c in modprobe systemctl sysctl iptables make apt-get git; do
  printf '#!/usr/bin/env bash\necho "%s $*" >>"$CALLS"\n' "$c" >"$TMP/bin/$c"
done
printf '#!/usr/bin/env bash\necho "awg $*" >>"$CALLS"\necho "$AWG_VERSION"\n' >"$TMP/bin/awg"
# stop also logs the conf it sees, to prove the stop comes before the conf is replaced.
printf '#!/usr/bin/env bash\necho "systemctl $* conf=$(cat "$ROOT/etc/geoirb-vpn/awg-exit.conf" 2>/dev/null)" >>"$CALLS"\n' >"$TMP/bin/systemctl"
chmod +x "$TMP/bin"/*

# Package as deploy-exit.sh lays it out: flat directory, the files of its own cp line.
mkdir "$TMP/pkg"
PKG="$(sed -n '/^cp "\$ROOT\/deploy\/exit\/install.sh"/,/"\$TMP\/pkg\/"$/p' "$DIR/../../scripts/deploy-exit.sh" | grep -o '\$ROOT/deploy/[^" ]*' | sed 's|^\$ROOT/deploy/||')"
for f in $PKG; do cp "$DIR/../$f" "$TMP/pkg/"; done
echo "[Interface]" >"$TMP/pkg/awg-exit.conf"

run() { # run <awg version output>
  : >"$TMP/calls"
  CALLS="$TMP/calls" AWG_VERSION="$1" ROOT="$TMP/root" PATH="$TMP/bin:$PATH" bash "$TMP/pkg/install.sh" >/dev/null 2>&1
}

mkdir -p "$TMP/root/etc/geoirb-vpn"; echo OLD >"$TMP/root/etc/geoirb-vpn/awg-exit.conf"
run "amneziawg-tools $TAG"
check "install exits 0" "0" "$?"
CONF="$TMP/root/etc/geoirb-vpn/awg-exit.conf"
check "conf content" "[Interface]" "$(cat "$CONF")"
check "conf mode" "600" "$(mode "$CONF")"
check "unit installed" "1" "$([[ -f "$TMP/root/etc/systemd/system/geoirb-awg-exit.service" ]] && echo 1)"
check "sysctl file installed" "1" "$([[ -f "$TMP/root/etc/sysctl.d/99-geoirb-vpn.conf" ]] && echo 1)"
# After a reboot the conntrack settings need the module loaded before systemd-sysctl.
check "conntrack loaded at boot, hash size set" "nf_conntrack options nf_conntrack hashsize=16384" \
  "$(grep -v '^#' "$TMP/root/etc/modules-load.d/nf_conntrack.conf" "$TMP/root/etc/modprobe.d/nf_conntrack.conf" 2>&1 | cut -d: -f2- | tr '\n' ' ' | sed 's/ $//')"
check "conntrack loaded before sysctl" "1" "$([[ "$(grep -n '^modprobe nf_conntrack' "$TMP/calls" | cut -d: -f1)" -lt "$(grep -n '^sysctl ' "$TMP/calls" | cut -d: -f1)" ]] && echo 1)"
check "enable called" "1" "$(grep -c '^systemctl enable.* geoirb-awg-exit' "$TMP/calls")"
check "start called" "1" "$(grep -c '^systemctl start geoirb-awg-exit' "$TMP/calls")"
check "stop before start" "1" "$([[ "$(grep -n '^systemctl stop geoirb-awg-exit' "$TMP/calls" | cut -d: -f1)" -lt "$(grep -n '^systemctl start geoirb-awg-exit' "$TMP/calls" | cut -d: -f1)" ]] && echo 1)"
check "stop saw the old conf" "1" "$(grep -c '^systemctl stop geoirb-awg-exit.service conf=OLD' "$TMP/calls")"
check "awg-tools skipped on pinned tag" "0" "$(grep -c '^\(make\|git\|apt-get\)' "$TMP/calls")"

run "amneziawg-tools v0.0.1"
check "awg-tools built on other version" "1" "$(grep -c '^make .*install' "$TMP/calls")"
check "build uses WITH_WGQUICK" "1" "$(grep -c 'WITH_WGQUICK=yes' "$TMP/calls")"
check "build deps installed" "1" "$(grep -c '^apt-get install.*build-essential git' "$TMP/calls")"
check "pinned tag cloned" "1" "$(grep -c "^git clone.*$TAG" "$TMP/calls")"

rm "$TMP/pkg/awg-exit.conf"
run "amneziawg-tools $TAG"
check "missing conf fails" "1" "$([[ $? -ne 0 ]] && echo 1)"

[[ $FAILS -eq 0 ]] || { echo "$FAILS failed"; exit 1; }
