.PHONY: up down build test test-race test-integration test-live llm-check lint fmt clean docker-build

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

llm-check:
	@bash -c 'set -e; set -a; [ -f .env ] && . ./.env; set +a; \
	if [ -z "$$ANTHROPIC_API_KEY" ] && [ -z "$$GEMINI_API_KEY" ] && [ -z "$$DEEPSEEK_API_KEY" ]; then \
		echo "ERRO: defina ANTHROPIC_API_KEY, GEMINI_API_KEY ou DEEPSEEK_API_KEY"; exit 1; \
	fi; \
	test -n "$$MODEL_ANALYSIS" || (echo "ERRO: MODEL_ANALYSIS não definido" && exit 1); \
	test -n "$$MODEL_RESPONSE" || (echo "ERRO: MODEL_RESPONSE não definido" && exit 1); \
	test -n "$$MODEL_NUDGE" || (echo "ERRO: MODEL_NUDGE não definido" && exit 1); \
	go test -v -tags=llm_live -run TestLive_SmokeLLM ./internal/brain'

test-live: llm-check
	go test -v -tags=llm_live ./internal/brain/...

lint:
	go vet ./...
	go run ./cmd/brain pack validate packs/realestate packs/support

fmt:
	go fmt ./...

docker-build:
	docker build -t whatshelper-brain:latest .

clean:
	rm -rf $(BIN_DIR)
