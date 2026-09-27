#!/usr/bin/env bash
# Runs the server backup now and lists the kept archives.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
remote "sudo systemctl start geoirb-vpn-bot-backup.service && sudo journalctl -u geoirb-vpn-bot-backup.service -n 3 --no-pager -o cat && sudo ls -lh /var/backups/geoirb-vpn-bot"
