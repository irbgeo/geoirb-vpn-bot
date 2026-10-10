#!/usr/bin/env bash
# Tests deploy/exit/install.sh with ROOT=<tmp> and fake commands on PATH.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$DIR/../../scripts/testlib.sh"

TAG="$(sed -n 's/^AWG_TOOLS_TAG="${AWG_TOOLS_TAG-\([^}]*\)}".*/\1/p' "$DIR/../awg-tools.sh")"
COMMIT="$(sed -n 's/^AWG_TOOLS_COMMIT="${AWG_TOOLS_COMMIT-\([^}]*\)}".*/\1/p' "$DIR/../awg-tools.sh")"

mkdir "$TMP/bin"
# Every fake logs its name and args; `awg --version` prints $AWG_VERSION.
for c in modprobe systemctl sysctl iptables make apt-get git; do
  printf '#!/usr/bin/env bash\necho "%s $*" >>"$CALLS"\n' "$c" >"$TMP/bin/$c"
done
printf '#!/usr/bin/env bash\necho "awg $*" >>"$CALLS"\necho "$AWG_VERSION"\n' >"$TMP/bin/awg"
# git: `rev-parse HEAD` prints $GIT_HEAD, the commit the cloned tag points to.
printf '#!/usr/bin/env bash\necho "git $*" >>"$CALLS"\n[[ "$*" != *rev-parse* ]] || echo "$GIT_HEAD"\n' >"$TMP/bin/git"
# stop also logs the conf it sees, to prove the stop comes before the conf is replaced.
printf '#!/usr/bin/env bash\necho "systemctl $* conf=$(cat "$ROOT/etc/geoirb-vpn/awg-exit.conf" 2>/dev/null)" >>"$CALLS"\n' >"$TMP/bin/systemctl"
chmod +x "$TMP/bin"/*

# Package as deploy-exit.sh lays it out: flat directory, the files of its own cp line.
mkdir "$TMP/pkg"
PKG="$(sed -n '/^cp "\$ROOT\/deploy\/exit\/install.sh"/,/"\$TMP\/pkg\/"$/p' "$DIR/../../scripts/deploy-exit.sh" | grep -o '\$ROOT/deploy/[^" ]*' | sed 's|^\$ROOT/deploy/||')"
for f in $PKG; do cp "$DIR/../$f" "$TMP/pkg/"; done
echo "[Interface]" >"$TMP/pkg/awg-exit.conf"

run() { # run <awg version output> [commit of the cloned tag]
  : >"$TMP/calls"
  CALLS="$TMP/calls" AWG_VERSION="$1" GIT_HEAD="${2:-$COMMIT}" ROOT="$TMP/root" PATH="$TMP/bin:$PATH" bash "$TMP/pkg/install.sh" >/dev/null 2>&1
}

mkdir -p "$TMP/root/etc/geoirb-vpn"; echo OLD >"$TMP/root/etc/geoirb-vpn/awg-exit.conf"
run "amneziawg-tools $TAG"
check "install exits 0" "0" "$?"
CONF="$TMP/root/etc/geoirb-vpn/awg-exit.conf"
check "conf content" "[Interface]" "$(cat "$CONF")"
check "conf mode" "600" "$(mode "$CONF")"
check "unit installed" "1" "$([[ -f "$TMP/root/etc/systemd/system/geoirb-awg-exit.service" ]] && echo 1)"
FWS=/usr/local/sbin/geoirb-exit-fw.sh
check "firewall script installed, runnable" "755" "$(mode "$TMP/root$FWS")"
check "the conf template calls it at that path" "PostUp = $FWS up %i
PostDown = $FWS down %i" "$(grep -E '^Post(Up|Down)' "$DIR/awg-exit.conf.tmpl")"
check "sysctl file installed" "1" "$([[ -f "$TMP/root/etc/sysctl.d/99-geoirb-vpn.conf" ]] && echo 1)"
# After a reboot the conntrack settings need the module loaded before systemd-sysctl.
check "conntrack loaded at boot, hash size set" "nf_conntrack options nf_conntrack hashsize=16384" \
  "$(grep -v '^#' "$TMP/root/etc/modules-load.d/nf_conntrack.conf" "$TMP/root/etc/modprobe.d/nf_conntrack.conf" 2>&1 | cut -d: -f2- | tr '\n' ' ' | sed 's/ $//')"
check "conntrack loaded before sysctl" "1" "$([[ "$(grep -n '^modprobe nf_conntrack' "$TMP/calls" | cut -d: -f1)" -lt "$(grep -n '^sysctl ' "$TMP/calls" | cut -d: -f1)" ]] && echo 1)"
check "enable called" "1" "$(grep -c '^systemctl enable.* geoirb-awg-exit' "$TMP/calls")"
# The modprobe file alone waits for the next load of the module (a reboot):
# the running kernel gets the hash size too. The run above had no such file
# (module not loaded) and still exited 0.
HS="$TMP/root/sys/module/nf_conntrack/parameters/hashsize"
check "no live hash size file: not created" "0" "$([[ -e "$HS" ]] && echo 1 || echo 0)"
check "start called" "1" "$(grep -c '^systemctl start geoirb-awg-exit' "$TMP/calls")"
check "stop before start" "1" "$([[ "$(grep -n '^systemctl stop geoirb-awg-exit' "$TMP/calls" | cut -d: -f1)" -lt "$(grep -n '^systemctl start geoirb-awg-exit' "$TMP/calls" | cut -d: -f1)" ]] && echo 1)"
check "stop saw the old conf" "1" "$(grep -c '^systemctl stop geoirb-awg-exit.service conf=OLD' "$TMP/calls")"
check "awg-tools skipped on pinned tag" "0" "$(grep -c '^\(make\|git\|apt-get\)' "$TMP/calls")"

mkdir -p "$(dirname "$HS")"; echo 7680 >"$HS"
run "amneziawg-tools $TAG"
check "install with the module loaded exits 0" "0" "$?"
check "live hash size follows the shipped file" "16384" "$(cat "$HS")"

run "amneziawg-tools v0.0.1"
check "same conf again: tunnel not stopped, only made sure it runs" "0 1" \
  "$(grep -c '^systemctl stop geoirb-awg-exit' "$TMP/calls") $(grep -c '^systemctl start geoirb-awg-exit' "$TMP/calls")"
check "awg-tools built on other version" "1" "$(grep -c '^make .*install' "$TMP/calls")"
check "build uses WITH_WGQUICK" "1" "$(grep -c 'WITH_WGQUICK=yes' "$TMP/calls")"
check "build deps installed, after an index update" "1 1" "$(grep -c '^apt-get install.*build-essential git' "$TMP/calls") $([[ "$(grep -n '^apt-get update' "$TMP/calls" | cut -d: -f1)" -lt "$(grep -n '^apt-get install' "$TMP/calls" | cut -d: -f1)" ]] && echo 1)"
check "pinned tag cloned" "1" "$(grep -c "^git clone.*$TAG" "$TMP/calls")"
check "pinned commit is a full hash" "40" "$(printf '%s' "$COMMIT" | tr -d -c '0-9a-f' | wc -c | tr -d ' ')"

# The tag was moved to another commit: nothing is built, the tunnel is not touched.
run "amneziawg-tools v0.0.1" 0000000000000000000000000000000000000000
check "moved tag fails" "1" "$([[ $? -ne 0 ]] && echo 1)"
check "moved tag: no build, no tunnel stop" "0" "$(grep -c '^\(make\|systemctl\)' "$TMP/calls")"

rm "$TMP/pkg/awg-exit.conf"
run "amneziawg-tools $TAG"
check "missing conf fails" "1" "$([[ $? -ne 0 ]] && echo 1)"

finish
