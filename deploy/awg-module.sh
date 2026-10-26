#!/usr/bin/env bash
# Loads the AmneziaWG kernel module. When it is missing, installs
# amneziawg-dkms from ppa:amnezia/ppa first, with the headers of the running
# kernel (dkms builds the module against them). Run as root, by
# deploy/install.sh and deploy/exit/install.sh.
set -euo pipefail

if modprobe amneziawg 2>/dev/null; then exit 0; fi

kernel="$(uname -r)"
apt-get update -qq # a host with an old package index fails the install
apt-get install -y -qq software-properties-common "linux-headers-$kernel"
add-apt-repository -y ppa:amnezia/ppa
apt-get install -y -qq amneziawg-dkms
modprobe amneziawg || {
  echo "error: amneziawg-dkms is installed, but the module does not load: dkms may not build for kernel $kernel (see: dkms status)" >&2
  exit 1
}
