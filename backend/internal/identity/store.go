package identity

import (
	"context"
	"time"
)

// Store is identity's persistence port (02 §2: modules own their interfaces;
// the postgres adapter lives in identity/postgres). One port over four small
// tables (users/sessions/user_config/projects); splitting it would scatter
// one cohesive adapter across several interfaces.
//
//nolint:interfacebloat // see comment above
type Store interface {
	// UpsertUser finds-or-creates by GitHubID, refreshing login/name/avatar
	// on every login (GitHub users can rename).
	UpsertUser(ctx context.Context, u User) (User, error)
	// GetUser returns ErrNotFound for an unknown id.
	GetUser(ctx context.Context, id string) (User, error)

	InsertSession(ctx context.Context, s Session) error
	// GetSession returns ErrNotFound for unknown hashes; expiry is the
	// service's business rule, so expired rows ARE returned.
	GetSession(ctx context.Context, tokenHash string) (Session, error)
	// TouchSession extends expiry (sliding window, 11 §2).
	TouchSession(ctx context.Context, tokenHash string, expiresAt time.Time) error
	DeleteSession(ctx context.Context, tokenHash string) error
	// GetSessionUser resolves a session's user in one call.
	GetSessionUser(ctx context.Context, tokenHash string) (Session, User, error)

	// GetUserConfig returns a zero-value UserConfig (not ErrNotFound) when the
	// user has never written config — callers treat absent as all-unset.
	GetUserConfig(ctx context.Context, userID string) (UserConfig, error)
	UpsertUserConfig(ctx context.Context, cfg UserConfig) error

	// GetProjectByOwner returns ErrNotFound before onboarding creates it.
	GetProjectByOwner(ctx context.Context, ownerUserID string) (Project, error)
	UpsertProject(ctx context.Context, p Project) (Project, error)
}
