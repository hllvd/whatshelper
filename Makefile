.PHONY: up down build test test-race test-integration lint fmt clean docker-build

APP_NAME := brain
BIN_DIR := bin

up:
	docker compose up -d --build

down:
	docker compose down

build:
	mkdir -p $(BIN_DIR)
	go build -trimpath -o $(BIN_DIR)/brain ./cmd/brain
	go build -trimpath -o $(BIN_DIR)/connectors ./cmd/connectors

test:
	go test -v ./...

test-race:
	go test -v -race ./...

test-integration:
	go test -v -tags=integration ./internal/store/...

lint:
	go vet ./...
	go run ./cmd/brain pack validate packs/realestate packs/support

fmt:
	go fmt ./...

docker-build:
	docker build -t whatshelper-brain:latest .

clean:
	rm -rf $(BIN_DIR)
