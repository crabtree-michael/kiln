#!/usr/bin/env bash
# Amika lifecycle script (see .amika/config.toml [lifecycle]).
#
# Installs the project's dependencies inside the sandbox so a coding agent lands
# in a ready-to-work checkout without any manual `make setup`. It is the exact
# work `make setup` does (frontend pnpm deps + backend Go modules), plus the two
# dev tools `make setup` only *prints* install commands for, so the hard gate
# (`make check`) runs with no follow-up.
#
# Wired as BOTH setup_script and start_script because Kiln's pool-and-recreate
# worker lifecycle (docs/specs/05 §4) takes two boot paths:
#   * initial create  -> Amika runs setup_script once, on a fresh repo clone
#   * resume from auto-stop -> Amika runs start_script on the persisted workspace
# The base snapshot bakes the toolchain (Go 1.26, Node 22, pnpm) and a warm
# dependency cache, so on a resume this is a fast, idempotent no-op; on a fresh
# clone whose lockfiles have drifted past the snapshot, it re-syncs them.
#
# Core dependency installs are hard requirements: if they fail, exit non-zero so
# Amika refuses to start the container rather than hand the agent a broken tree.
# The two dev tools are best-effort — a transient install hiccup there should not
# brick the whole sandbox. Handles no secrets and prints none.
set -euo pipefail

# setup_script/start_script run with cwd = the agent working dir ($AMIKA_AGENT_CWD).
# Fall back to the git toplevel, then the conventional path, then cwd.
repo_root="${AMIKA_AGENT_CWD:-}"
if [ -z "$repo_root" ]; then
  repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
fi
if [ -z "$repo_root" ]; then
  repo_root="$HOME/workspace/kiln"
fi
[ -d "$repo_root" ] || repo_root="$(pwd)"
cd "$repo_root"
echo "amika-setup: installing dependencies in $repo_root"

# --- Frontend: pnpm deps (make setup: `cd frontend && pnpm install`) -----------
if [ -d frontend ]; then
  if command -v pnpm >/dev/null 2>&1; then
    echo "amika-setup: frontend -> pnpm install"
    (cd frontend && pnpm install)
  else
    echo "amika-setup: WARNING pnpm not on PATH — is Node 22 + pnpm baked into the snapshot?" >&2
  fi
else
  echo "amika-setup: no frontend/ dir — skipping pnpm install" >&2
fi

# --- Backend: Go modules (make setup: `cd backend && go mod download`) ----------
if [ -d backend ]; then
  if command -v go >/dev/null 2>&1; then
    echo "amika-setup: backend -> go mod download"
    (cd backend && go mod download)
  else
    echo "amika-setup: WARNING go not on PATH — is the Go toolchain baked into the snapshot?" >&2
  fi
else
  echo "amika-setup: no backend/ dir — skipping go mod download" >&2
fi

# --- Dev tools the hard gate needs (make setup only prints these) --------------
# Best-effort: install only when missing so a resume is a no-op, and never fail
# the boot over them.
if command -v go >/dev/null 2>&1; then
  if ! command -v golangci-lint >/dev/null 2>&1; then
    # Pinned to the version CI installs, read out of the workflow so the two
    # cannot drift. The v2 module path matters: `@latest` on the unversioned
    # path resolves to v1.64.x, which cannot read this repo's `version: "2"`
    # .golangci.yml — it installs cleanly and then fails at lint time.
    golangci_version="$(grep -oE 'v2\.[0-9]+\.[0-9]+' .github/workflows/check.yml 2>/dev/null | head -1 || true)"
    [ -n "$golangci_version" ] || golangci_version="v2.12.2"
    echo "amika-setup: installing golangci-lint $golangci_version"
    curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
      | sh -s -- -b "$(go env GOPATH)/bin" "$golangci_version" >/dev/null \
      || echo "amika-setup: WARNING golangci-lint install failed (make lint will be unavailable)" >&2
  fi
  # Pinned to the version that generated the checked-in wire package, read from
  # that file's own header so the two cannot drift. `@latest` is the same trap as
  # golangci-lint above: v2.8.0 renames enum constants relative to the v2.7.1 that
  # produced backend/internal/wire, so `make schema` on an @latest box emits a
  # diff unrelated to the agent's change and `make schema-verify` goes red on a
  # clean tree. CI has no schema step, so nothing upstream would catch it.
  oapi_version="$(grep -oE 'oapi-codegen/v2 version v[0-9]+\.[0-9]+\.[0-9]+' backend/internal/wire/generated.go 2>/dev/null \
    | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true)"
  [ -n "$oapi_version" ] || oapi_version="v2.7.1"
  have_oapi="$(oapi-codegen --version 2>/dev/null | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true)"
  if [ "$have_oapi" != "$oapi_version" ]; then
    echo "amika-setup: installing oapi-codegen $oapi_version"
    go install "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$oapi_version" \
      || echo "amika-setup: WARNING oapi-codegen install failed (make schema will be unavailable)" >&2
  fi
fi

# --- Local services ------------------------------------------------------------
# The box has no systemd, so postgres and dockerd are not running after a create
# or a resume — they have to be started once per boot or the integration half of
# the gate silently skips and `make up` has no daemon. Best-effort by design.
if [ -x scripts/amika/start-services.sh ]; then
  scripts/amika/start-services.sh || true
fi

echo "amika-setup: done"
