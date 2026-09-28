# Local defaults match a Homebrew Postgres on port 5433; override on the command line.
DATABASE_URL      ?= postgres://localhost:5433/tracker_mcp?sslmode=disable
TEST_DATABASE_URL ?= postgres://localhost:5433/tracker_mcp_test?sslmode=disable
VERSION           ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BINARY            := bin/tracker-mcp

.PHONY: build run-stdio run-http test test-unit lint fmt db-create tidy

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) ./cmd/tracker-mcp

run-stdio: build
	DATABASE_URL="$(DATABASE_URL)" ./$(BINARY) --transport stdio

run-http: build
	DATABASE_URL="$(DATABASE_URL)" ./$(BINARY) --transport http --http-addr 127.0.0.1:8080

## Creates the dev and test databases (idempotent).
db-create:
	createdb -p 5433 tracker_mcp 2>/dev/null || true
	createdb -p 5433 tracker_mcp_test 2>/dev/null || true

## Unit + end-to-end tests. The e2e suite RESETS the schema of TEST_DATABASE_URL.
test:
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -race -count=1 -p 1 ./...

test-unit:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

fmt:
	golangci-lint fmt ./...

tidy:
	go mod tidy
