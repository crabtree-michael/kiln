// Package postgres is identity's store adapter (mirrors board/postgres): the
// only code that touches the users/sessions/user_config/projects tables. It
// owns the migrations in ./migrations and is wired in at the composition root
// (02 §2, backend/cmd/kiln).
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
	"github.com/crabtree-michael/kiln/backend/internal/pgutil"
)

// userColumns is the canonical projection for a user row, shared by every
// SELECT/RETURNING so scanUser can read them positionally.
const userColumns = `id, github_id, github_login, display_name, avatar_url, created_at`

// projectColumns is the canonical projection for a project row.
const projectColumns = `id, owner_user_id, name, repo_url, amika_snapshot, ` +
	`brain_model, worker_count, amika_secrets, created_at`

// Store implements identity.Store over Postgres.
type Store struct {
	db *sql.DB
}

var _ identity.Store = (*Store)(nil)

// New wraps an open connection pool; migrations are applied separately at
// startup (mirrors board/postgres.New).
func New(db *sql.DB) *Store { return &Store{db: db} }

// UpsertUser reconciles a GitHub identity into a single users row. Two callers
// reach it with different notions of github_id: real OAuth logins carry the
// authoritative GitHub numeric id, while the bootstrap-from-env and dev
// find-or-create (EnsureUser) carry a synthetic fnv id standing in for the
// login (11 §7). The stable bridge across both is github_login, so a plain
// ON CONFLICT (github_id) upsert is wrong: a bootstrapped operator's later real
// sign-in arrives with a *different* id under the *same* login and collides on
// the github_login unique constraint. The reconcile is therefore three-step,
// in one transaction:
//  1. adopt by github_id — a repeat login, and login renames (login refreshed);
//  2. else adopt by github_login — a synthetic-id row (bootstrap/dev) being
//     claimed by its real OAuth id, stamping the authoritative id onto the row
//     so the owner keeps their existing project/config;
//  3. else insert — first sighting of this identity.
func (s *Store) UpsertUser(ctx context.Context, u identity.User) (identity.User, error) {
	var out identity.User
	txErr := pgutil.InTx(ctx, s.db, nil, func(tx *sql.Tx) error {
		var err error
		out, err = scanUser(tx.QueryRowContext(ctx, `
			UPDATE users
			   SET github_login = lower($2), display_name = $3, avatar_url = $4
			 WHERE github_id = $1
			RETURNING `+userColumns,
			u.GitHubID, u.GitHubLogin, u.DisplayName, u.AvatarURL))
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		out, err = scanUser(tx.QueryRowContext(ctx, `
			UPDATE users
			   SET github_id = $1, display_name = $3, avatar_url = $4
			 WHERE github_login = lower($2)
			RETURNING `+userColumns,
			u.GitHubID, u.GitHubLogin, u.DisplayName, u.AvatarURL))
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		out, err = scanUser(tx.QueryRowContext(ctx, `
			INSERT INTO users (github_id, github_login, display_name, avatar_url)
			VALUES ($1, lower($2), $3, $4)
			RETURNING `+userColumns,
			u.GitHubID, u.GitHubLogin, u.DisplayName, u.AvatarURL))
		return err
	})
	if txErr != nil {
		return identity.User{}, fmt.Errorf("identity/postgres: upsert user: %w", txErr)
	}
	return out, nil
}

// EnsureUserByLogin find-or-creates by github_login without touching an existing
// row. The ON CONFLICT DO UPDATE is a deliberate no-op (sets github_login to its
// current value) purely so RETURNING yields the existing row — the synthetic
// bootstrap/dev github_id (and any real id UpsertUser previously stamped) and
// the stored profile are all left intact (11 §7).
func (s *Store) EnsureUserByLogin(ctx context.Context, u identity.User) (identity.User, error) {
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO users (github_id, github_login, display_name, avatar_url)
		VALUES ($1, lower($2), $3, $4)
		ON CONFLICT (github_login) DO UPDATE SET github_login = users.github_login
		RETURNING `+userColumns,
		u.GitHubID, u.GitHubLogin, u.DisplayName, u.AvatarURL)
	out, err := scanUser(row)
	if err != nil {
		return identity.User{}, fmt.Errorf("identity/postgres: ensure user: %w", err)
	}
	return out, nil
}

// GetUser returns ErrNotFound for an unknown id.
func (s *Store) GetUser(ctx context.Context, id string) (identity.User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.User{}, identity.ErrNotFound
	}
	if err != nil {
		return identity.User{}, fmt.Errorf("identity/postgres: get user: %w", err)
	}
	return u, nil
}

// InsertSession persists a new session row.
func (s *Store) InsertSession(ctx context.Context, sess identity.Session) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES ($1, $2, $3)`,
		sess.TokenHash, sess.UserID, sess.ExpiresAt); err != nil {
		return fmt.Errorf("identity/postgres: insert session: %w", err)
	}
	return nil
}

// GetSession returns ErrNotFound for unknown hashes; expiry is the service's
// business rule, so expired rows ARE returned.
func (s *Store) GetSession(ctx context.Context, tokenHash string) (identity.Session, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT token_hash, user_id, created_at, expires_at FROM sessions WHERE token_hash = $1`, tokenHash)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.Session{}, identity.ErrNotFound
	}
	if err != nil {
		return identity.Session{}, fmt.Errorf("identity/postgres: get session: %w", err)
	}
	return sess, nil
}

// TouchSession extends expiry (sliding window, 11 §2).
func (s *Store) TouchSession(ctx context.Context, tokenHash string, expiresAt time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET expires_at = $2 WHERE token_hash = $1`, tokenHash, expiresAt); err != nil {
		return fmt.Errorf("identity/postgres: touch session: %w", err)
	}
	return nil
}

// DeleteSession removes a session row.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("identity/postgres: delete session: %w", err)
	}
	return nil
}

// GetSessionUser resolves a session's user in one call.
func (s *Store) GetSessionUser(ctx context.Context, tokenHash string) (identity.Session, identity.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT s.token_hash, s.user_id, s.created_at, s.expires_at,
		       u.id, u.github_id, u.github_login, u.display_name, u.avatar_url, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1`, tokenHash)

	var (
		sess     identity.Session
		user     identity.User
		userID   string
		githubID int64
	)
	if err := row.Scan(&sess.TokenHash, &sess.UserID, &sess.CreatedAt, &sess.ExpiresAt,
		&userID, &githubID, &user.GitHubLogin, &user.DisplayName, &user.AvatarURL, &user.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return identity.Session{}, identity.User{}, identity.ErrNotFound
		}
		return identity.Session{}, identity.User{}, fmt.Errorf("identity/postgres: get session user: %w", err)
	}
	user.ID = userID
	user.GitHubID = githubID
	return sess, user, nil
}

// GetUserConfig returns a zero-value UserConfig (not ErrNotFound) when the
// user has never written config — callers treat absent as all-unset.
func (s *Store) GetUserConfig(ctx context.Context, userID string) (identity.UserConfig, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT user_id, anthropic_api_key_enc, amika_api_key_enc, github_auth_token_enc,
		       amika_claude_cred_id
		FROM user_config WHERE user_id = $1`, userID)

	var cfg identity.UserConfig
	if err := row.Scan(&cfg.UserID, &cfg.AnthropicKeyEnc, &cfg.AmikaKeyEnc, &cfg.GitHubTokenEnc,
		&cfg.AmikaClaudeCredID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return identity.UserConfig{UserID: userID}, nil
		}
		return identity.UserConfig{}, fmt.Errorf("identity/postgres: get user config: %w", err)
	}
	return cfg, nil
}

// UpsertUserConfig writes all columns; the service does the read-modify-write
// merge (partial updates never reach here).
func (s *Store) UpsertUserConfig(ctx context.Context, cfg identity.UserConfig) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO user_config (user_id, anthropic_api_key_enc, amika_api_key_enc,
		                         github_auth_token_enc, amika_claude_cred_id, updated_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (user_id) DO UPDATE
		  SET anthropic_api_key_enc = EXCLUDED.anthropic_api_key_enc,
		      amika_api_key_enc     = EXCLUDED.amika_api_key_enc,
		      github_auth_token_enc = EXCLUDED.github_auth_token_enc,
		      amika_claude_cred_id  = EXCLUDED.amika_claude_cred_id,
		      updated_at            = now()`,
		cfg.UserID, cfg.AnthropicKeyEnc, cfg.AmikaKeyEnc, cfg.GitHubTokenEnc,
		cfg.AmikaClaudeCredID); err != nil {
		return fmt.Errorf("identity/postgres: upsert user config: %w", err)
	}
	return nil
}

// GetProjectByOwner returns ErrNotFound before onboarding creates it.
func (s *Store) GetProjectByOwner(ctx context.Context, ownerUserID string) (identity.Project, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE owner_user_id = $1`, ownerUserID)
	p, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.Project{}, identity.ErrNotFound
	}
	if err != nil {
		return identity.Project{}, fmt.Errorf("identity/postgres: get project by owner: %w", err)
	}
	return p, nil
}

// GetProject returns ErrNotFound for an unknown projects.id.
func (s *Store) GetProject(ctx context.Context, id string) (identity.Project, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE id = $1`, id)
	p, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.Project{}, identity.ErrNotFound
	}
	if err != nil {
		return identity.Project{}, fmt.Errorf("identity/postgres: get project: %w", err)
	}
	return p, nil
}

// ListProjects returns every project ordered by created_at (stable startup
// ordering for the runtime's per-project registry). The named err return lets
// the deferred rows.Close failure surface (the board/postgres idiom).
func (s *Store) ListProjects(ctx context.Context) (_ []identity.Project, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+projectColumns+` FROM projects ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("identity/postgres: list projects: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("identity/postgres: close projects: %w", cerr)
		}
	}()

	var out []identity.Project
	for rows.Next() {
		p, serr := scanProject(rows)
		if serr != nil {
			return nil, serr
		}
		out = append(out, p)
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, fmt.Errorf("identity/postgres: iterate projects: %w", rerr)
	}
	return out, nil
}

// UpsertProject creates or updates the owner's project in place (one project
// per owner in phase 1).
func (s *Store) UpsertProject(ctx context.Context, p identity.Project) (identity.Project, error) {
	secrets, err := marshalAmikaSecrets(p.AmikaSecrets)
	if err != nil {
		return identity.Project{}, err
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO projects (owner_user_id, name, repo_url, amika_snapshot, brain_model, worker_count, amika_secrets)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (owner_user_id) DO UPDATE
		  SET name = EXCLUDED.name, repo_url = EXCLUDED.repo_url,
		      amika_snapshot = EXCLUDED.amika_snapshot,
		      brain_model = EXCLUDED.brain_model, worker_count = EXCLUDED.worker_count,
		      amika_secrets = EXCLUDED.amika_secrets
		RETURNING `+projectColumns,
		p.OwnerUserID, p.Name, p.RepoURL, p.AmikaSnapshot, p.BrainModel, p.WorkerCount, secrets)
	out, err := scanProject(row)
	if err != nil {
		return identity.Project{}, fmt.Errorf("identity/postgres: upsert project: %w", err)
	}
	return out, nil
}

// rowScanner is the minimal *sql.Row/*sql.Rows surface scanUser/scanProject
// need (mirrors board/postgres's pgutil.RowScanner).
type rowScanner interface {
	Scan(dest ...any) error
}

// scanSession reads one session row. A sql.ErrNoRows is returned wrapped so
// callers can still detect it with errors.Is while satisfying wrapcheck.
func scanSession(r rowScanner) (identity.Session, error) {
	var sess identity.Session
	if err := r.Scan(&sess.TokenHash, &sess.UserID, &sess.CreatedAt, &sess.ExpiresAt); err != nil {
		return identity.Session{}, fmt.Errorf("identity/postgres: scan session: %w", err)
	}
	return sess, nil
}

// scanUser reads one user row. A sql.ErrNoRows is returned wrapped so callers
// can still detect it with errors.Is while satisfying wrapcheck.
func scanUser(r rowScanner) (identity.User, error) {
	var u identity.User
	if err := r.Scan(&u.ID, &u.GitHubID, &u.GitHubLogin, &u.DisplayName, &u.AvatarURL, &u.CreatedAt); err != nil {
		return identity.User{}, fmt.Errorf("identity/postgres: scan user: %w", err)
	}
	return u, nil
}

// scanProject reads one project row. A sql.ErrNoRows is returned wrapped so
// callers can still detect it with errors.Is while satisfying wrapcheck.
func scanProject(r rowScanner) (identity.Project, error) {
	var p identity.Project
	var secrets []byte
	if err := r.Scan(&p.ID, &p.OwnerUserID, &p.Name, &p.RepoURL, &p.AmikaSnapshot,
		&p.BrainModel, &p.WorkerCount, &secrets, &p.CreatedAt); err != nil {
		return identity.Project{}, fmt.Errorf("identity/postgres: scan project: %w", err)
	}
	parsed, err := unmarshalAmikaSecrets(secrets)
	if err != nil {
		return identity.Project{}, err
	}
	p.AmikaSecrets = parsed
	return p, nil
}

// amikaSecretRow is the jsonb element shape for projects.amika_secrets. Name and
// value are BOTH stored as AES-GCM ciphertext (encoding/json renders the []byte
// as base64), so nothing readable lands on disk (02 §8, 11 §3 D7). The JSON keys
// are pinned here, never coupled to the domain type's field names.
type amikaSecretRow struct {
	NameEnc  []byte `json:"name_enc"`
	ValueEnc []byte `json:"value_enc"`
}

// marshalAmikaSecrets encodes the list for the jsonb column; a nil/empty list
// becomes "[]" so the NOT NULL DEFAULT invariant holds on every write.
func marshalAmikaSecrets(secrets []identity.AmikaSecret) ([]byte, error) {
	rows := make([]amikaSecretRow, len(secrets))
	for i, s := range secrets {
		rows[i] = amikaSecretRow{NameEnc: s.NameEnc, ValueEnc: s.ValueEnc}
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("identity/postgres: marshal amika secrets: %w", err)
	}
	return b, nil
}

// unmarshalAmikaSecrets decodes the jsonb column; an empty "[]" yields a nil
// slice (the zero value callers treat as "no secrets").
func unmarshalAmikaSecrets(b []byte) ([]identity.AmikaSecret, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var rows []amikaSecretRow
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, fmt.Errorf("identity/postgres: unmarshal amika secrets: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([]identity.AmikaSecret, len(rows))
	for i, r := range rows {
		out[i] = identity.AmikaSecret{NameEnc: r.NameEnc, ValueEnc: r.ValueEnc}
	}
	return out, nil
}
