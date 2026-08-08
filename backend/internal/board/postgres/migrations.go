package postgres

import "embed"

// Migrations holds the board module's schema migrations, embedded into the
// binary so kiln ships as a single static binary (backend/Dockerfile) with no
// loose migration files to find at runtime — the composition root applies them
// at startup (04 §5). The module owns these files (see store.go).
//
//go:embed migrations/*.sql
var Migrations embed.FS

// MigrationsKey is this module's stable prefix in the schema_migrations
// ledger — the original relative path, kept verbatim so the ledger is
// unaffected by the move to go:embed. The composition root and the
// integration suites both key off this, so neither can drift from the
// other about what a database already has.
const MigrationsKey = "internal/board/postgres/migrations"
