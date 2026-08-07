// Package beta owns the private-beta request list: the people who reached Kiln,
// completed GitHub auth, and were turned away because their login isn't on the
// allowlist (11 §2). It is a tiny write-mostly module — the api module records
// one login here from the OAuth callback's rejection path (same boundary rule as
// push: the module owns its Store port, the postgres adapter lives in
// beta/postgres, and the composition root wires them). There is no read side in
// v1; the list is inspected out-of-band (psql) when deciding who to admit.
//
// It used to be fed by a landing-page email form instead. That form is gone: the
// landing page's two buttons both go through GitHub, so the record now writes
// itself and no one has to be asked for an address they've already proven.
package beta

import (
	"context"
	"time"
)

// Signup is one recorded request to get into the beta. ID and CreatedAt are
// store-assigned. GitHubLogin is unique so a repeat rejection is idempotent —
// someone who tries to sign in three times is one row, not three.
//
// Email is empty for everyone recorded through the GitHub gate; it carries only
// the addresses collected by the retired landing-page form, kept because they
// are real interest. Every row has one identifier or the other.
type Signup struct {
	ID          int64
	GitHubLogin string
	Email       string
	CreatedAt   time.Time
}

// Store persists private-beta requests (02 §2: the module owns its port; the
// postgres adapter lives in beta/postgres). Save is idempotent on the login — a
// rejected user retrying sign-in is a no-op, never a duplicate row or an error
// (mirrors push.Store.Save's upsert-on-endpoint). That idempotence is what lets
// the callback record unconditionally without first asking whether it already
// has them.
type Store interface {
	Save(ctx context.Context, githubLogin string) error
}
