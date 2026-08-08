#!/usr/bin/env bash
# Recreate the integration-test database (kiln_test) empty.
#
# kiln_test holds only test scratch: every integration suite truncates the
# tables it owns on setup, and nothing outside the gate reads it. Dropping it is
# therefore cheap and safe — the schema is rebuilt from the modules' embedded
# migrations on the next `make check`, recorded in the shared schema_migrations
# ledger (internal/testutil/migrate.go).
#
# Reach for this when the gate fails with `column "..." does not exist` against a
# table that plainly has that column in the repo: the database is older than the
# migration that added it. Provisioning also calls this automatically for a
# kiln_test that predates the ledger entirely.
#
# Never point this at the live `kiln` database — it is a different database on
# the same cluster and this script never touches it by name.
set -euo pipefail

log() { printf 'reset-test-db: %s\n' "$*"; }
warn() { printf 'reset-test-db: WARNING %s\n' "$*" >&2; }

# The sandbox cluster lives on 5433, not 5432 — compose's `db` service publishes
# 5432 on the host, so the two would collide (see the local-environment skill).
TEST_DB_PORT="${TEST_DB_PORT:-5433}"

if ! sudo -n true 2>/dev/null; then
  warn "no passwordless sudo — cannot reach the postgres superuser"
  exit 1
fi
if ! pg_isready -q -p "$TEST_DB_PORT" 2>/dev/null; then
  warn "postgres is not accepting connections on $TEST_DB_PORT — run 'make services' first"
  exit 1
fi

# --force disconnects anything still attached (a stray `psql`, a crashed test
# run) rather than failing with "database is being accessed by other users".
log "dropping and recreating kiln_test on port $TEST_DB_PORT"
sudo -n -u postgres dropdb -p "$TEST_DB_PORT" --if-exists --force kiln_test
sudo -n -u postgres createdb -p "$TEST_DB_PORT" -O kiln kiln_test
log "done — the next 'make check' rebuilds the schema from the embedded migrations"
