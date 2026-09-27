.PHONY: lint test build deploy backup backup-pull

lint:
	golangci-lint run --fix

# Go tests (store tests need Mongo on localhost:27017) and the script tests.
test:
	go test ./...
	./scripts/server-env_test.sh

# Linux binary for the VPN server.
build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bot ./cmd/bot

# Build and install/update the bot on the VPN server (systemd).
deploy:
	./scripts/deploy.sh

# Run the server backup now and list the archives.
backup:
	./scripts/backup-now.sh

# Copy the newest server backup to ./backups/ (run it from time to time, or
# from a local cron: a lost VPS must not take its backups with it).
backup-pull:
	./scripts/backup-pull.sh
