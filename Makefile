SHELL := /bin/bash
.DEFAULT_GOAL := help

-include .env
export

DATABASE_URL ?= postgres://valence:valence@localhost:5432/valence?sslmode=disable

BIN := $(CURDIR)/bin
export PATH := $(BIN):$(PATH)

SQLC_VERSION := v1.31.1
BUF_VERSION := v1.73.0
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_CONNECT_GO_VERSION := v1.21.0
GOOSE_VERSION := v3.27.0

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: tools
tools: $(BIN)/sqlc $(BIN)/buf $(BIN)/protoc-gen-go $(BIN)/protoc-gen-connect-go $(BIN)/goose ## Install pinned code generators into ./bin

$(BIN)/sqlc:
	GOBIN=$(BIN) go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
$(BIN)/buf:
	GOBIN=$(BIN) go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
$(BIN)/protoc-gen-go:
	GOBIN=$(BIN) go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
$(BIN)/protoc-gen-connect-go:
	GOBIN=$(BIN) go install connectrpc.com/connect/cmd/protoc-gen-connect-go@$(PROTOC_GEN_CONNECT_GO_VERSION)
$(BIN)/goose:
	GOBIN=$(BIN) go install github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)

apps/web/node_modules/.bin/protoc-gen-es:
	pnpm install --frozen-lockfile

.PHONY: gen
gen: tools apps/web/node_modules/.bin/protoc-gen-es ## Regenerate sqlc and protobuf code
	sqlc generate
	cd proto && buf lint && buf generate

.PHONY: gen-check
gen-check: gen ## Fail if generated code is out of date
	@git diff --exit-code -- gen apps/web/src/gen || (echo "Generated code is stale: run 'make gen' and commit the result" && exit 1)
	@test -z "$$(git status --porcelain -- gen apps/web/src/gen)" || (git status --porcelain -- gen apps/web/src/gen && echo "Untracked generated files: run 'make gen' and commit the result" && exit 1)

.PHONY: migrate
migrate: $(BIN)/goose ## Apply database migrations to DATABASE_URL
	goose -dir db/migrations postgres "$(DATABASE_URL)" up

.PHONY: migrate-down
migrate-down: $(BIN)/goose ## Roll back the latest migration
	goose -dir db/migrations postgres "$(DATABASE_URL)" down

.PHONY: migrate-new
migrate-new: $(BIN)/goose ## Create a migration: make migrate-new name=add_contests
	@test -n "$(name)" || (echo "usage: make migrate-new name=<snake_case_name>" && exit 1)
	goose -dir db/migrations -s create $(name) sql

.PHONY: seed
seed: ## Import every problem under problems/examples
	go run ./services/api/cmd/valence-admin problem import problems/examples/*/

.PHONY: run-api
run-api: ## Run the API (auto-migrates in dev)
	go run ./services/api/cmd/api

.PHONY: run-worker
run-worker: ## Run a judge worker
	go run ./services/judge-worker/cmd/judge-worker

.PHONY: test
test: ## Run Go tests (Postgres tests use VALENCE_TEST_DATABASE_URL or Docker)
	go test -race ./...

.PHONY: test-short
test-short: ## Run Go unit tests only
	go test -short ./...

.PHONY: fmt
fmt: ## Format Go code
	gofmt -w $$(git ls-files '*.go' | grep -v '^gen/')

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: sandbox-test
sandbox-test: ## Run the sandbox escape suite against the isolate provider (Linux, root)
	go test -count=1 -v ./judge/sandbox-tests/...
