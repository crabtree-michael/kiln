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
	// UpsertUser reconciles a real OAuth identity: adopt by github_id (repeat
	// login / rename), else adopt an existing row by github_login (claiming a
	// synthetic-id bootstrap/dev row with the authoritative id), else insert.
	UpsertUser(ctx context.Context, u User) (User, error)
	// EnsureUserByLogin find-or-creates by github_login WITHOUT overwriting an
	// existing row's github_id or profile — the bootstrap/dev path (11 §7),
	// whose synthetic id must never clobber a real id UpsertUser already wrote.
	EnsureUserByLogin(ctx context.Context, u User) (User, error)
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
	// GetProject returns ErrNotFound for an unknown projects.id.
	GetProject(ctx context.Context, id string) (Project, error)
	// ListProjects returns every project ordered by created_at (stable startup
	// ordering for the runtime's per-project registry).
	ListProjects(ctx context.Context) ([]Project, error)
	UpsertProject(ctx context.Context, p Project) (Project, error)
}
