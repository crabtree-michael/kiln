package postgres

import "embed"

// Migrations holds the agent module's schema migrations, embedded into the
// binary so kiln ships as a single static binary (backend/Dockerfile) with no
// loose migration files to find at runtime — the composition root applies them
// at startup (04 §5). The module owns these files (see store.go).
//
//go:embed migrations/*.sql
var Migrations embed.FS
