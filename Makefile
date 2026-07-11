# Kiln hard gate + task runner (docs/specs/02 §4).
#
# `make check` is the wall: lint -> type-check/build -> unit -> integration.
# Red means you cannot land. Git hooks (make hooks) and CI both run this.
#
# Toolchain: Go 1.23+, golangci-lint, oapi-codegen (backend); Node 22 + pnpm
# (frontend). `make setup` installs what it can.

BACKEND  := backend
FRONTEND := frontend
SCHEMA   := schema

.DEFAULT_GOAL := help

## ----------------------------------------------------------------------------
## Meta
## ----------------------------------------------------------------------------

.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: setup
setup: ## Install dependencies and dev tools
	cd $(FRONTEND) && pnpm install
	cd $(BACKEND) && go mod download
	@echo "Install golangci-lint + oapi-codegen if missing:"
	@echo "  go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"
	@echo "  go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest"

.PHONY: hooks
hooks: ## Install the git pre-commit / pre-push hard-gate hooks
	git config core.hooksPath .githooks
	@echo "git hooks installed -> .githooks (pre-commit, pre-push)"

## ----------------------------------------------------------------------------
## The hard gate
## ----------------------------------------------------------------------------

.PHONY: check
check: lint typecheck test ## Full hard gate: lint + type-check/build + tests

.PHONY: lint
lint: lint-backend lint-frontend ## Lint + format-check both surfaces

.PHONY: lint-backend
lint-backend:
	cd $(BACKEND) && gofmt -l . && test -z "$$(gofmt -l .)"
	cd $(BACKEND) && golangci-lint run ./...

.PHONY: lint-frontend
lint-frontend:
	cd $(FRONTEND) && pnpm run lint
	cd $(FRONTEND) && pnpm run format

.PHONY: typecheck
typecheck: ## Compile backend, type-check frontend
	cd $(BACKEND) && go build ./...
	cd $(FRONTEND) && pnpm run typecheck

.PHONY: test
test: test-backend test-frontend ## Unit + integration tests both surfaces

.PHONY: test-backend
test-backend:
	cd $(BACKEND) && go test ./...
	# Integration tests share one mutable kiln_test DB and reset it with
	# TRUNCATE (board, runtime, api-tenancy, cmd/kiln all clear overlapping
	# tables — e.g. outbox, tickets, workers). `go test ./...` runs packages
	# concurrently by default, so those resets race and wipe each other's rows
	# mid-test. -p 1 runs the integration packages one at a time (02 §14: the DB
	# is shared, never isolated per package), which is the only safe order.
	cd $(BACKEND) && go test -tags=integration -p 1 ./...

.PHONY: test-frontend
test-frontend:
	cd $(FRONTEND) && pnpm run test

.PHONY: e2e
e2e: ## End-to-end test: drive the real web client against a running stack (02 §4a; hits real services)
	cd tests && pnpm test

.PHONY: up-keyless
up-keyless: ## Bring the stack up in keyless mode (all providers mocked; no API keys)
	docker compose -f docker-compose.yml -f docker-compose.keyless.yml up --build -d
	@echo "keyless stack up — frontend http://localhost:5173, mock-stt :7071, mock-push :7072"

.PHONY: down-keyless
down-keyless: ## Tear down the keyless stack + volumes
	docker compose -f docker-compose.yml -f docker-compose.keyless.yml down -v

.PHONY: e2e-keyless
e2e-keyless: ## Keyless e2e: the @keyless specs against the mocked stack — no API keys, CI-runnable (design docs/keyless-e2e-tests-design.md)
	cd tests && pnpm test --grep @keyless

## ----------------------------------------------------------------------------
## Contract + build + run
## ----------------------------------------------------------------------------

.PHONY: schema
schema: ## Regenerate Go + TS types from schema/openapi.yaml
	cd $(FRONTEND) && pnpm exec openapi-typescript ../$(SCHEMA)/openapi.yaml -o src/schema/generated.ts
	cd $(SCHEMA) && oapi-codegen -config oapi-codegen.yaml openapi.yaml

.PHONY: schema-verify
schema-verify: schema ## Fail if checked-in generated types are stale
	git diff --exit-code -- $(FRONTEND)/src/schema $(BACKEND)/internal/wire \
		|| { echo "generated types are stale: run 'make schema' and commit"; exit 1; }

.PHONY: build
build: ## Production build of both surfaces
	cd $(BACKEND) && go build -o bin/kiln ./cmd/kiln
	cd $(FRONTEND) && pnpm run build

.PHONY: up
up: ## Bring the whole system up locally (Docker Compose)
	docker compose up --build

.PHONY: down
down: ## Tear the local stack down
	docker compose down -v
