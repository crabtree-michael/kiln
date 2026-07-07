// Package beta owns the pre-launch beta-signup list: the emails collected by the
// marketing landing page's "Join the beta" form. It is a tiny write-mostly
// module — the client's POST /api/beta-signup lands one email here through the
// api module (same boundary rule as push: the module owns its Store port, the
// postgres adapter lives in beta/postgres, and the composition root wires them).
// There is no read side in v1; the list is inspected out-of-band (psql).
package beta

import (
	"context"
	"time"
)

// Signup is one collected beta-interest email. ID and CreatedAt are
// store-assigned; Email is unique so a repeat submit is idempotent.
type Signup struct {
	ID        int64
	Email     string
	CreatedAt time.Time
}

// Store persists beta-signup emails (02 §2: the module owns its port; the
// postgres adapter lives in beta/postgres). Save is idempotent on Email — a
// visitor submitting the same address twice is a no-op, never a duplicate row
// or an error (mirrors push.Store.Save's upsert-on-endpoint).
type Store interface {
	Save(ctx context.Context, email string) error
}
