#!/usr/bin/env bash
# Local development only: acts as `docker` on the VPN server, so the bot
# can run on a laptop against the real container:
#   DOCKER_BIN=scripts/dev-docker.sh go run ./cmd/bot
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
remote "sudo docker $(printf '%q ' "$@")"
