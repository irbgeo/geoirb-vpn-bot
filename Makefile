.PHONY: lint test build deploy deploy-exit backup backup-pull diagram diagram-open

lint:
	golangci-lint run --fix

# Go tests (store tests need Mongo on localhost:27017) and the script tests.
test:
	go test -cover ./...
	./scripts/lib_test.sh
	./scripts/server-env_test.sh
	./scripts/tunnel-keys_test.sh
	./deploy/exit/install_test.sh
	./deploy/install_test.sh
	./deploy/awg0-init_test.sh
	./deploy/import-peers_test.sh
	./scripts/import-peers_test.sh
	./deploy/ru-nets_test.sh
	./deploy/vpn-routes_test.sh
	./deploy/backup_test.sh
	./scripts/backup-pull_test.sh

# Linux binary for the VPN server (deploy.sh builds with OUT=<its package>).
OUT ?= bot
build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$(OUT)" ./cmd/bot

# Build and install/update the bot and the host VPN on the RU server (systemd).
deploy:
	./scripts/deploy.sh

# Install the exit side of the tunnel on secret/exit-access.yaml's host.
deploy-exit:
	./scripts/deploy-exit.sh

# Run the server backup now and list the archives.
backup:
	./scripts/backup-now.sh

# Copy the newest server backup to ./backups/ (run it from time to time, or
# from a local cron: a lost VPS must not take its backups with it).
backup-pull:
	./scripts/backup-pull.sh


diagram-architecture-open:
	open docs/diagrams/architecture.html

diagram-network-open:
	open docs/diagrams/network.html

