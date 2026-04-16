.PHONY: build test test-int lint check backtest clean tidy

BINARY := cerebro
CONFIG_DIR := configs
DATABASE_URL := postgresql://postgres.azzplqrjsmeueedehmpm:d%25%4079ff%2fazYAKgD@aws-1-ap-southeast-1.pooler.supabase.com:6543/postgres

build:
	go build -ldflags="-s -w" -o $(BINARY) ./cmd/cerebro

test:
	go test ./...

test-int:
	go test -tags=integration ./...

lint:
	golangci-lint run ./...

check:
	go run ./cmd/cerebro check --dry-run --config-dir=$(CONFIG_DIR)

backtest:
	go run ./cmd/cerebro backtest \
		--strategy=trend_following \
		--data=testdata/fixtures/btc_1m.csv \
		--from=2024-01-01 \
		--to=2024-12-31

migrate-up:
	migrate -source file://scripts/migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -source file://scripts/migrations -database "$(DATABASE_URL)" down

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)
