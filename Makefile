# Kiln hard gate + task runner (docs/specs/02 §4).
#
# `make check` is the wall: contract freshness -> lint -> type-check/build ->
# unit -> integration. Red means you cannot land. Git hooks (make hooks) and CI
# both run this.
#
# Toolchain: Go 1.23+, golangci-lint (backend); Node 22 + pnpm (frontend).
# `make setup` installs what it can; oapi-codegen needs no install (see below).

BACKEND  := backend
FRONTEND := frontend
SCHEMA   := schema
TESTS    := tests

# Codegen tooling is version-pinned, not floating: `make schema` must produce
# byte-identical output for everyone, or `schema-verify` (in the hard gate
# below) turns into a coin flip that fails on whoever regenerated last with a
# different tool build. The Go side pins here; the TS side pins as an exact
# version in frontend/package.json (openapi-typescript, no caret).
#
# Bumping either is a deliberate two-step: change the pin, run `make schema`,
# and commit the regenerated output in the same commit.
OAPI_CODEGEN_VERSION := v2.7.1
OAPI_CODEGEN := go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)

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
	# oapi-codegen is not installed: `make schema` runs it via `go run` at the
	# pinned OAPI_CODEGEN_VERSION, so there is no local build to drift.
	@echo "Install golangci-lint if missing:"
	@echo "  go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"

.PHONY: sandbox
sandbox: ## Provision a bare dev box: toolchain, test database, docker, hooks, deps
	scripts/amika/provision.sh

.PHONY: services
services: ## Start the local services (postgres for integration tests, docker for the stack)
	scripts/amika/start-services.sh

.PHONY: test-db-reset
test-db-reset: ## Recreate the integration-test database (kiln_test) empty — it holds only test scratch
	scripts/amika/reset-test-db.sh

.PHONY: hooks
hooks: ## Install the git pre-commit / pre-push hard-gate hooks
	git config core.hooksPath .githooks
	@echo "git hooks installed -> .githooks (pre-commit, pre-push)"

## ----------------------------------------------------------------------------
## The hard gate
## ----------------------------------------------------------------------------

.PHONY: check
check: schema-verify lint typecheck test ## Full hard gate: contract freshness + lint + type-check/build + tests

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
	cd $(SCHEMA) && $(OAPI_CODEGEN) -config oapi-codegen.yaml openapi.yaml

# In `make check` (02 §3: schema and both generated sides version together
# atomically) — a forgotten regen is a contract drift between client and server,
# and until this ran in the gate it passed CI silently while both surfaces
# compiled fine against their own stale copy.
#
# Note this regenerates in place, so a failure leaves the fresh output already in
# your working tree: read the diff, then commit it.
.PHONY: schema-verify
schema-verify: schema ## Fail if checked-in generated types are stale
	@git diff --exit-code -- $(FRONTEND)/src/schema $(BACKEND)/internal/wire \
		|| { echo "generated types are stale: schema/openapi.yaml changed without a regen. The diff above is the fresh output, now in your working tree — commit it alongside the schema change."; exit 1; }

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
