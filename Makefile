.PHONY: run build test lint fmt tidy check docker-up docker-down migrate-help

run:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

test:
	go test ./... -count=1

# Integration tests need a throwaway Postgres; they skip themselves without it.
test-integration:
	INTRODUCE_TEST_DB="postgres://introduce:introduce@localhost:5433/introduce_test?sslmode=disable" \
		go test ./... -count=1 -tags=integration

fmt:
	gofmt -w cmd internal

lint:
	go vet ./...

tidy:
	go mod tidy

check: fmt lint test

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down
