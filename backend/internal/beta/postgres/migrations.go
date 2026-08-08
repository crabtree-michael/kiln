package postgres

import "embed"

// Migrations holds the beta module's schema migrations, embedded so kiln ships
// as a single static binary (same pattern as board/runtime/agent/identity/push).
//
//go:embed migrations/*.sql
var Migrations embed.FS

// MigrationsKey is this module's stable prefix in the schema_migrations
// ledger — the original relative path, kept verbatim so the ledger is
// unaffected by the move to go:embed. The composition root and the
// integration suites both key off this, so neither can drift from the
// other about what a database already has.
const MigrationsKey = "internal/beta/postgres/migrations"
