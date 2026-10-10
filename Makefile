.PHONY: run build test tidy migrate-up migrate-down docker-build docker-up docker-down

# Локальный запуск (нужны PostgreSQL и переменные окружения)
run:
	JWT_SECRET=$(or $(JWT_SECRET),dev-secret-change-me) go run ./cmd/server

build:
	CGO_ENABLED=0 go build -o bin/chatter ./cmd/server

test:
	go test ./...

tidy:
	go mod tidy

migrate-up:
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path migrations -database "$(DATABASE_URL)" down -all

docker-build:
	docker compose build

docker-up:
	docker compose up -d

docker-down:
	docker compose down

# --- HTTPS (Caddy + Let's Encrypt) ---
https-up:
	docker compose -f docker-compose.yml -f docker-compose.caddy.yml up -d --build

https-logs:
	docker compose logs caddy
