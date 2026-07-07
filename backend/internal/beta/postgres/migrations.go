package postgres

import "embed"

// Migrations holds the beta module's schema migrations, embedded so kiln ships
// as a single static binary (same pattern as board/runtime/agent/identity/push).
//
//go:embed migrations/*.sql
var Migrations embed.FS
