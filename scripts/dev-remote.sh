#!/usr/bin/env bash
# Local development only: runs a command on the VPN server as root, so the
# bot can run on a laptop against the real host:
#   AWG_EXEC=scripts/dev-remote.sh go run ./cmd/bot
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
remote "sudo $(printf '%q ' "$@")"
