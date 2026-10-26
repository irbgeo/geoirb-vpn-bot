#!/usr/bin/env bash
# Loads the AmneziaWG kernel module. When it is missing, installs
# amneziawg-dkms from ppa:amnezia/ppa first, with the headers of the running
# kernel (dkms builds the module against them). Run as root, by
# deploy/install.sh and deploy/exit/install.sh.
set -euo pipefail
# The PPA package builds a kernel module as root: take the version that was
# checked (on the exit server, 2026-10-26, Ubuntu 24.04), not whatever the
# PPA has today. When the PPA drops it the install fails here; then check a
# newer one and change this line (AWG_DKMS_VERSION= takes the current one).
AWG_DKMS_VERSION="${AWG_DKMS_VERSION-1.0.0-0~202609061402+4569c4c~ubuntu24.04.1}"

if modprobe amneziawg 2>/dev/null; then exit 0; fi

kernel="$(uname -r)"
apt-get update -qq # a host with an old package index fails the install
apt-get install -y -qq software-properties-common "linux-headers-$kernel"
add-apt-repository -y ppa:amnezia/ppa
apt-get install -y -qq "amneziawg-dkms${AWG_DKMS_VERSION:+=$AWG_DKMS_VERSION}"
modprobe amneziawg || {
  echo "error: amneziawg-dkms is installed, but the module does not load: dkms may not build for kernel $kernel (see: dkms status)" >&2
  exit 1
}
