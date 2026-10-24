.PHONY: all build run test docker-build docker-up docker-down clean

all: build

build:
	@echo "==> Building Go Backend Binary..."
	@cd server && go build -v -o bin/server ./cmd/server

run: build
	@echo "==> Starting HttpSMS Gateway Server locally..."
	@cd server && DB_PATH=./data/httpsms.db WEB_DIR=../web OTA_DIR=./data/ota ./bin/server

test:
	@echo "==> Running Go unit tests..."
	@cd server && go test -v ./...

docker-build:
	@echo "==> Building Docker image..."
	@docker build -f docker/Dockerfile -t httpsms-gateway:latest .

docker-up:
	@echo "==> Launching HttpSMS Gateway via Docker Compose..."
	@docker compose up -d

docker-down:
	@echo "==> Stopping Docker Compose services..."
	@docker compose down

clean:
	@echo "==> Cleaning build artifacts..."
	@rm -rf server/bin server/data data

