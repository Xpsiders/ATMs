.PHONY: build run test test-race lint fmt sqlc migrate-up migrate-down seed docker-up docker-down openapi-lint

build:
	go build ./...

run:
	go run ./cmd/api

test:
	go test ./...

test-race:
	go test -race ./...

lint:
	golangci-lint run

fmt:
	go fmt ./...

sqlc:
	sqlc generate

migrate-up:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down

seed:
	go run ./scripts/seed $(ARGS)

docker-up:
	docker compose up --build

docker-down:
	docker compose down

openapi-lint:
	npx --yes @redocly/cli lint api/openapi.yaml
