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
TESTS    := tests

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
	# The layout gate (`make test-layout`) drives a real headless browser, so the
	# tests package and its Chromium are part of the hard gate's toolchain now,
	# not just the deliberately-run e2e suite's.
	cd $(TESTS) && pnpm install && pnpm run install-browser
	@echo "Install golangci-lint + oapi-codegen if missing:"
	@echo "  go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"
	@echo "  go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest"

.PHONY: sandbox
sandbox: ## Provision a bare dev box: toolchain, test database, docker, hooks, deps
	scripts/amika/provision.sh

.PHONY: services
services: ## Start the local services (postgres for integration tests, docker for the stack)
	scripts/amika/start-services.sh

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
test: test-backend test-frontend test-layout ## Unit + integration tests both surfaces, plus the layout gate

.PHONY: test-backend
test-backend:
	cd $(BACKEND) && go test ./...
	# Integration tests share one mutable kiln_test DB and reset it with
	# TRUNCATE (board, runtime, api-tenancy, cmd/kiln all clear overlapping
	# tables — e.g. outbox, tickets, workers). `go test ./...` runs packages
	# concurrently by default, so those resets race and wipe each other's rows
	# mid-test. -p 1 runs the integration packages one at a time (02 §14: the DB
	# is shared, never isolated per package), which is the only safe order.
	#
	# -race because this is where the multi-instance concurrency tests live: the
	# leader election (internal/leader) and the two-Services-over-one-store turn
	# test (internal/agent) both run several goroutines against shared state on
	# purpose. Asserting the outcome without the detector would check the count
	# and miss the race that produced it.
	cd $(BACKEND) && go test -race -tags=integration -p 1 ./...

.PHONY: test-frontend
test-frontend:
	cd $(FRONTEND) && pnpm run test

.PHONY: test-layout
test-layout: ## Layout gate: computed geometry of both shells in a real headless browser
	# jsdom performs no layout, so `test-frontend` above can see which elements
	# render and never where they end up — which is how the same "Show earlier" /
	# toast overlap shipped five times with the unit gate green throughout. This
	# suite measures boxes and asks the browser what paints at a point.
	#
	# It is in the gate, unlike `e2e` below, because it needs no stack and no
	# keys: it serves the client from its own dev server and fulfils every /api
	# call from fixtures. ~1 min. See tests/layout/harness.ts.
	cd $(TESTS) && pnpm run test:layout

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
