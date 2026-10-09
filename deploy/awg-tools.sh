#!/usr/bin/env bash
# Installs amneziawg-tools (awg, awg-quick) at the pinned tag, built from source.
# Does nothing when `awg --version` already shows that tag. Run as root.
set -euo pipefail
AWG_TOOLS_TAG="v3.1.20260812" # github.com/amnezia-vpn/amneziawg-tools
[[ -n "$AWG_TOOLS_TAG" ]] || { echo "set AWG_TOOLS_TAG" >&2; exit 1; }

if awg --version 2>/dev/null | grep -qF "$AWG_TOOLS_TAG"; then exit 0; fi

apt-get install -y build-essential git
src="$(mktemp -d)"
trap 'rm -rf "$src"' EXIT
git clone --depth 1 --branch "$AWG_TOOLS_TAG" https://github.com/amnezia-vpn/amneziawg-tools "$src"
make -C "$src/src"
make -C "$src/src" install WITH_WGQUICK=yes
