#!/usr/bin/env bash
# Installs amneziawg-tools (awg, awg-quick) at the pinned tag, built from source.
# Does nothing when `awg --version` already shows that tag. Run as root.
set -euo pipefail
AWG_TOOLS_TAG="${AWG_TOOLS_TAG-v3.1.20260812}" # github.com/amnezia-vpn/amneziawg-tools
# The commit that tag pointed to when it was pinned: a tag can be moved, and
# this code is built and installed as root. Change both lines together.
AWG_TOOLS_COMMIT="${AWG_TOOLS_COMMIT-ee0f0a9aa34ff0a0da4b3433b9512781cfe02843}"
[[ -n "$AWG_TOOLS_TAG" ]] || { echo "set AWG_TOOLS_TAG" >&2; exit 1; }

if awg --version 2>/dev/null | grep -qF "${AWG_TOOLS_TAG#v}"; then exit 0; fi

apt-get update -qq # a host with an old package index fails the install
apt-get install -y build-essential git
src="$(mktemp -d)"
trap 'rm -rf "$src"' EXIT
git clone --depth 1 --branch "$AWG_TOOLS_TAG" https://github.com/amnezia-vpn/amneziawg-tools "$src"
head="$(git -C "$src" rev-parse HEAD)"
if [[ "$head" != "$AWG_TOOLS_COMMIT" ]]; then
  echo "error: tag $AWG_TOOLS_TAG is commit $head, expected $AWG_TOOLS_COMMIT (a moved tag?). Nothing was built." >&2
  exit 1
fi
make -C "$src/src"
make -C "$src/src" install WITH_WGQUICK=yes
