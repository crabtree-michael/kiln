package postgres

import "embed"

// Migrations holds the steward module's schema migrations, embedded into the
// binary so kiln ships as a single static binary with no loose migration files
// to find at runtime — the composition root applies them at startup.
//
//go:embed migrations/*.sql
var Migrations embed.FS
