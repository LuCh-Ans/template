-include .env
export

MIGRATIONS_DIR := migrations
MIGRATIONS_DIR := migrations
IMAGE ?= trip-service:local

.PHONY: run build migrate migrate-down migrate-status psql generate test docker-build docker-run

run:
	go run ./cmd/trip-service

build:
	go build -o bin/trip-service ./cmd/trip-service

migrate:
	go tool goose -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)" up

migrate-down:
	go tool goose -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)" down

migrate-status:
	go tool goose -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)" status

psql:
	psql "$(DATABASE_URL)"

generate:
	@mkdir -p internal/generated
	go tool oapi-codegen -config oapi-codegen.yaml contracts/openapi/trip-service.openapi.yaml

test:
	go test -race ./...


docker-build:
	docker build -f deploy/Dockerfile -t $(IMAGE) .

docker-run:
	docker run --rm -p 8080:8080 \
		-e HTTP_ADDR=:8080 \
		-e DATABASE_URL="$(subst localhost,host.docker.internal,$(DATABASE_URL))" \
		$(IMAGE)