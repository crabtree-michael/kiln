package postgres

import "embed"

// Migrations holds the push module's schema migrations, embedded so kiln ships
// as a single static binary (same pattern as board/runtime/agent/identity).
//
//go:embed migrations/*.sql
var Migrations embed.FS
