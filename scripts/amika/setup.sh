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
    echo "amika-setup: installing golangci-lint"
    go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest \
      || echo "amika-setup: WARNING golangci-lint install failed (make lint will be unavailable)" >&2
  fi
  if ! command -v oapi-codegen >/dev/null 2>&1; then
    echo "amika-setup: installing oapi-codegen"
    go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest \
      || echo "amika-setup: WARNING oapi-codegen install failed (make schema will be unavailable)" >&2
  fi
fi

echo "amika-setup: done"
