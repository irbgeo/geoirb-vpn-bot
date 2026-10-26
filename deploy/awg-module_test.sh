#!/usr/bin/env bash
# Tests deploy/awg-module.sh with fake commands on PATH.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$DIR/../scripts/testlib.sh"

mkdir "$TMP/bin"
for c in apt-get add-apt-repository; do
  printf '#!/usr/bin/env bash\necho "%s $*" >>"$CALLS"\n' "$c" >"$TMP/bin/$c"
done
printf '#!/usr/bin/env bash\necho 6.8.0-99-generic\n' >"$TMP/bin/uname"
# modprobe fails while $LOADS has fewer lines than $FAILS_BEFORE.
cat >"$TMP/bin/modprobe" <<'EOF'
#!/usr/bin/env bash
echo "modprobe $*" >>"$CALLS"
echo x >>"$LOADS"
[[ "$(wc -l <"$LOADS")" -gt "$FAILS_BEFORE" ]]
EOF
chmod +x "$TMP/bin"/*

run() { # run <modprobe failures before it loads> [env...]
  : >"$TMP/calls"
  : >"$TMP/loads"
  local fails="$1"
  shift
  env CALLS="$TMP/calls" LOADS="$TMP/loads" FAILS_BEFORE="$fails" PATH="$TMP/bin:$PATH" "$@" bash "$DIR/awg-module.sh" >/dev/null 2>&1
}
PIN="$(sed -n 's/^AWG_DKMS_VERSION="${AWG_DKMS_VERSION-\([^}]*\)}".*/\1/p' "$DIR/awg-module.sh")"

run 0
check "module already there: exits 0" "0" "$?"
check "module already there: nothing installed" "0" "$(grep -c '^\(apt-get\|add-apt-repository\)' "$TMP/calls")"

run 1
check "missing module: installed, then loaded" "0" "$?"
check "ppa added" "1" "$(grep -c '^add-apt-repository -y ppa:amnezia/ppa' "$TMP/calls")"
check "headers of the running kernel installed" "1" "$(grep -c '^apt-get install.*linux-headers-6.8.0-99-generic' "$TMP/calls")"
check "the pinned dkms version is installed" "1" "$(grep -c "^apt-get install.* amneziawg-dkms=$PIN" "$TMP/calls")"
check "the pin is set" "1" "$([[ -n "$PIN" ]] && echo 1)"
check "dkms package installed after the ppa" "1" \
  "$([[ "$(grep -n '^add-apt-repository' "$TMP/calls" | cut -d: -f1)" -lt "$(grep -n '^apt-get install.*amneziawg-dkms' "$TMP/calls" | cut -d: -f1)" ]] && echo 1)"
check "loaded after the install" "amneziawg" "$(tail -1 "$TMP/calls" | sed 's/^modprobe //')"

run 2
check "dkms built nothing for this kernel: fails" "1" "$([[ $? -ne 0 ]] && echo 1)"

run 1 AWG_DKMS_VERSION=
check "an empty pin takes the PPA's current version" "1" "$(grep -cE '^apt-get install -y -qq amneziawg-dkms$' "$TMP/calls")"

finish
