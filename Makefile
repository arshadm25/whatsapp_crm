# Load .env if present so the run targets get local settings.
ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: help up migrate run-api run-ingest run-worker web test test-db generate lint build images

help:
	@grep -E '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  %-12s %s\n", $$1, $$2}'

up: ## Start Postgres and Mailpit
	docker compose up -d

migrate: ## Apply database migrations
	go run ./cmd/ecogo migrate

run-api: ## Run the api on :8080
	go run ./cmd/ecogo api

run-ingest: ## Run the webhook receiver on :8081
	ECOGO_HTTP_ADDR=:8081 go run ./cmd/ecogo ingest

run-worker: ## Run the job worker (health on :8082)
	ECOGO_HTTP_ADDR=:8082 go run ./cmd/ecogo worker

web: ## Run the dashboard dev server on :5173
	cd web && npm run dev

test: ## Go unit tests (database tests skip without ECOGO_TEST_ADMIN_DATABASE_URL)
	go test ./...

test-db: ## All Go tests against the local Postgres
	ECOGO_TEST_ADMIN_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres go test -count=1 ./...

generate: ## Regenerate sqlc code after changing migrations or queries
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

lint: ## go vet, gofmt and dashboard type check
	go vet ./...
	test -z "$$(gofmt -l cmd internal)"
	cd web && npx tsc -b

build: ## Build the ecogo binary into bin/
	go build -o bin/ecogo ./cmd/ecogo

images: ## Build the backend and dashboard images
	docker build -t ecogo-whatsapp:dev .
	docker build -t ecogo-whatsapp-web:dev web
