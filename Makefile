.PHONY: build run test test-race docker-up docker-down docker-logs reconcile clean

BIN_DIR := bin
BIN_NAME := api
PORT ?= 8080
DB_URL ?= postgres://ledger:ledger@localhost:5432/ledger?sslmode=disable

build:
	@echo "Building binary..."
	go build -ldflags="-s -w" -o $(BIN_DIR)/$(BIN_NAME) ./cmd/api

run: build
	PORT=$(PORT) DB_URL="$(DB_URL)" ./$(BIN_DIR)/$(BIN_NAME)

test:
	@echo "Running tests..."
	go test -v ./...

test-race:
	@echo "Running tests with race detector..."
	go test -v -race ./...

docker-up:
	@echo "Starting services with Docker Compose..."
	docker compose up -d --build

docker-down:
	@echo "Stopping Docker Compose services..."
	docker compose down -v

docker-logs:
	docker compose logs -f api

reconcile:
	@echo "Reconciling ledger..."
	curl -s http://localhost:$(PORT)/admin/reconcile | jq .

clean:
	rm -rf $(BIN_DIR)
