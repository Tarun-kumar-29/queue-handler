.PHONY: build run test docker-build docker-up docker-down clean deps lint

# Variables
APP_NAME=queue-handler
DOCKER_COMPOSE_FILE=deployments/docker/docker-compose.yml
GO_VERSION=1.21

# Build the application
build:
	go build -o bin/$(APP_NAME) cmd/server/main.go

# Run locally (requires Redis running)
run:
	go run cmd/server/main.go

# Run with local Redis
run-with-redis:
	@echo "Starting Redis in background..."
	@docker run -d --name redis-local -p 6379:6379 redis:7-alpine || echo "Redis already running"
	@sleep 2
	@echo "Starting application..."
	@go run cmd/server/main.go

# Stop local Redis
stop-redis:
	@docker stop redis-local || true
	@docker rm redis-local || true

# Install dependencies
deps:
	go mod download
	go mod tidy

# Run tests
test:
	go test ./...

# Run tests with coverage
test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Lint code
lint:
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run

# Format code
fmt:
	go fmt ./...

# Docker commands
docker-build:
	docker-compose -f $(DOCKER_COMPOSE_FILE) build

docker-up:
	docker-compose -f $(DOCKER_COMPOSE_FILE) up -d

docker-down:
	docker-compose -f $(DOCKER_COMPOSE_FILE) down

docker-logs:
	docker-compose -f $(DOCKER_COMPOSE_FILE) logs -f

docker-restart:
	docker-compose -f $(DOCKER_COMPOSE_FILE) restart queue-handler

# Development commands
dev-setup: deps
	@echo "✅ Development environment setup complete"
	@echo "🚀 Run 'make docker-up' to start the complete stack"
	@echo "🎛️ Asynq Web UI will be available at http://localhost:8080"

dev-start: docker-up
	@echo "🎉 Queue Handler started!"
	@echo "📋 API Server: http://localhost:3000"
	@echo "🎛️ Asynq Web UI: http://localhost:8080"
	@echo "📊 Health Check: http://localhost:3000/health"

# Test API endpoints
test-api:
	@echo "🧪 Testing API endpoints..."
	@echo "1. Health Check:"
	@curl -s http://localhost:3000/health | jq '.' || echo "❌ Health check failed"
	@echo "\n2. Schedule Job:"
	@curl -s -X POST http://localhost:3000/api/schedule-job \
		-H "Content-Type: application/json" \
		-d '{"url": "https://httpbin.org/get", "method": "GET", "call_id": "test-001"}' | jq '.' || echo "❌ Job scheduling failed"
	@echo "\n3. Queue Stats:"
	@curl -s "http://localhost:3000/api/queue/stats?queue=default" | jq '.' || echo "❌ Queue stats failed"

# Production commands
prod-build:
	CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags '-w -s' -o bin/$(APP_NAME) cmd/server/main.go

prod-deploy: prod-build docker-build
	@echo "🚀 Production build complete"

# Monitoring
monitor:
	@echo "📊 Opening monitoring interfaces..."
	@echo "🎛️ Asynq Web UI: http://localhost:8080"
	@which open >/dev/null && open http://localhost:8080 || echo "Open http://localhost:8080 in your browser"

# Clean up
clean:
	rm -rf bin/
	docker-compose -f $(DOCKER_COMPOSE_FILE) down -v
	docker system prune -f

# Install Asynq CLI tool
install-asynq-cli:
	go install github.com/hibiken/asynq/tools/asynq@latest
	@echo "✅ Asynq CLI installed. Use 'asynq dash --redis-addr=localhost:6379' for monitoring"

# Help
help:
	@echo "Available commands:"
	@echo "  build          - Build the application binary"
	@echo "  run            - Run the application locally"
	@echo "  run-with-redis - Start Redis and run the application"
	@echo "  test           - Run tests"
	@echo "  test-coverage  - Run tests with coverage"
	@echo "  docker-up      - Start with Docker Compose"
	@echo "  docker-down    - Stop Docker Compose"
	@echo "  docker-logs    - View Docker logs"
	@echo "  dev-setup      - Setup development environment"
	@echo "  dev-start      - Start development stack"
	@echo "  test-api       - Test API endpoints"
	@echo "  monitor        - Open monitoring UI"
	@echo "  clean          - Clean up build artifacts and containers"
	@echo "  help           - Show this help message"