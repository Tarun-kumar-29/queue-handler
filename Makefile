.PHONY: build run stop clean logs test docker-build docker-run docker-stop

# Docker commands
docker-build:
	docker-compose -f deployments/docker/docker-compose.yml build

docker-run:
	docker-compose -f deployments/docker/docker-compose.yml up -d

docker-stop:
	docker-compose -f deployments/docker/docker-compose.yml down

docker-logs:
	docker-compose -f deployments/docker/docker-compose.yml logs -f

docker-restart:
	docker-compose -f deployments/docker/docker-compose.yml restart

docker-clean:
	docker-compose -f deployments/docker/docker-compose.yml down -v
	docker system prune -f

# Development commands
build:
	go build -o bin/queue-handler cmd/server/main.go

run:
	go run cmd/server/main.go

test:
	go test ./...

deps:
	go mod tidy
	go mod download

# Monitoring
monitor:
	@echo "Opening monitoring dashboard..."
	@echo "Asynq Monitor: http://localhost:8080"
	@echo "Queue Handler API: http://localhost:3000"

# Quick setup
setup: docker-build docker-run monitor

# Full cleanup
nuke: docker-clean
	docker volume rm queue-handler_redis_data 2>/dev/null || true