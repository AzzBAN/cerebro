.PHONY: help build build-go web web-install dev test test-int lint check backtest migrate-up migrate-down clean tidy

BINARY := cerebro
CONFIG_DIR := configs
DATABASE_URL ?= $(shell echo $$DATABASE_URL)

# help prints every documented target. Targets are documented by appending
# "## description" to their rule line; this recipe greps those out.
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# build compiles the frontend (embedded via //go:embed) then the Go binary.
# Use build-go to skip the frontend when iterating on backend-only changes
# (the previously-built dist/ is reused, or the "not built" placeholder shows).
build: web build-go ## Build frontend + Go binary

build-go: ## Build Go binary only (reuse existing dist/)
	go build -ldflags="-s -w" -o $(BINARY) ./cmd/cerebro

# web builds the Next.js dashboard into internal/adapter/web/dist so the Go
# binary can embed it. Requires Node.js + npm.
web: web-install ## Build the Next.js dashboard into the embed dir
	cd web && npm run build
	rm -rf internal/adapter/web/dist
	cp -r web/dist internal/adapter/web/dist

web-install: ## Install frontend npm dependencies
	cd web && npm install

# dev-demo runs the engine via "go run" against Binance Demo (real mainnet
# prices, virtual execution). It depends on `web` so the embedded dashboard is
# rebuilt + re-embedded first — frontend edits show up without a manual `make
# web`. Needs a Binance API key configured for the demo environment.
dev-demo: web ## Rebuild the web dashboard, then run the engine in demo mode (real prices, virtual fills)
	go run ./cmd/cerebro run --demo --config-dir=$(CONFIG_DIR)

# dev-paper runs fully offline with synthetic market data — no keys required.
dev-paper: ## Run the engine in paper mode via go run (offline, no keys)
	go run ./cmd/cerebro run --paper --config-dir=$(CONFIG_DIR)

test: ## Run unit tests
	go test ./...

test-int: ## Run integration tests (requires DB/Redis/testnet)
	go test -tags=integration ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

check: ## Dry-run config validation
	go run ./cmd/cerebro check --dry-run --config-dir=$(CONFIG_DIR)

backtest: ## Run a sample trend-following backtest
	go run ./cmd/cerebro backtest \
		--strategy=trend_following \
		--data=testdata/fixtures/btc_1m.csv \
		--from=2024-01-01 \
		--to=2024-12-31

migrate-up: ## Apply all pending DB migrations
	migrate -source file://scripts/migrations -database "$(DATABASE_URL)" up

migrate-down: ## Roll back one DB migration
	migrate -source file://scripts/migrations -database "$(DATABASE_URL)" down

tidy: ## Run go mod tidy
	go mod tidy

clean: ## Remove the built binary
	rm -f $(BINARY)
