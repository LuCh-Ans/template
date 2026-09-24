-include .env
export

MIGRATIONS_DIR := migrations

.PHONY: run build migrate migrate-down generate test

run:
	go run ./cmd/trip-service

build:
	go build -o bin/trip-service ./cmd/trip-service

migrate:
	go tool goose -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)" up

migrate-down:
	go tool goose -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)" down

generate:
	@echo "not configured yet"

test:
	go test -race ./...