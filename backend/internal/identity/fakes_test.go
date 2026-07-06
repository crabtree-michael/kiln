package identity_test

// Shared in-memory unit-test fakes for the identity module (mirrors
// board/fakes_test.go's convention: maps + one mutex, package identity_test).

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
	"github.com/crabtree-michael/kiln/backend/internal/identity/githubapi"
)

// fakeStore is an in-memory identity.Store.
type fakeStore struct {
	mu                sync.Mutex
	users             map[int64]identity.User // keyed by GitHubID
	sessions          map[string]identity.Session
	configs           map[string]identity.UserConfig
	projects          map[string]identity.Project // keyed by OwnerUserID
	seq               int
	touchSessionCalls int // how many times TouchSession was invoked, for negative-renewal assertions
}

var _ identity.Store = (*fakeStore)(nil)

func newFakeStore() *fakeStore {
	return &fakeStore{
		users:    map[int64]identity.User{},
		sessions: map[string]identity.Session{},
		configs:  map[string]identity.UserConfig{},
		projects: map[string]identity.Project{},
	}
}

// UpsertUser finds-or-creates by GitHubID, assigning ids "user-1", "user-2", etc.
func (s *fakeStore) UpsertUser(_ context.Context, u identity.User) (identity.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.users[u.GitHubID]
	if ok {
		u.ID = existing.ID
		u.CreatedAt = existing.CreatedAt
	} else {
		s.seq++
		u.ID = fmt.Sprintf("user-%d", s.seq)
		u.CreatedAt = time.Now()
	}
	s.users[u.GitHubID] = u
	return u, nil
}

// GetUser returns ErrNotFound for an unknown id.
func (s *fakeStore) GetUser(_ context.Context, id string) (identity.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if u.ID == id {
			return u, nil
		}
	}
	return identity.User{}, identity.ErrNotFound
}

func (s *fakeStore) InsertSession(_ context.Context, sess identity.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sess.TokenHash] = sess
	return nil
}

func (s *fakeStore) GetSession(_ context.Context, tokenHash string) (identity.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[tokenHash]
	if !ok {
		return identity.Session{}, identity.ErrNotFound
	}
	return sess, nil
}

func (s *fakeStore) TouchSession(_ context.Context, tokenHash string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchSessionCalls++
	sess, ok := s.sessions[tokenHash]
	if !ok {
		return identity.ErrNotFound
	}
	sess.ExpiresAt = expiresAt
	s.sessions[tokenHash] = sess
	return nil
}

// DeleteSession is idempotent-nil for unknown hashes, matching
// postgres.Store.DeleteSession (a DELETE with no matching row is not an error).
func (s *fakeStore) DeleteSession(_ context.Context, tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, tokenHash)
	return nil
}

func (s *fakeStore) GetSessionUser(_ context.Context, tokenHash string) (identity.Session, identity.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[tokenHash]
	if !ok {
		return identity.Session{}, identity.User{}, identity.ErrNotFound
	}
	for _, u := range s.users {
		if u.ID == sess.UserID {
			return sess, u, nil
		}
	}
	return identity.Session{}, identity.User{}, identity.ErrNotFound
}

// GetUserConfig returns a zero-value UserConfig (not ErrNotFound) when the
// user has never written config.
func (s *fakeStore) GetUserConfig(_ context.Context, userID string) (identity.UserConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, ok := s.configs[userID]
	if !ok {
		return identity.UserConfig{UserID: userID}, nil
	}
	return cfg, nil
}

func (s *fakeStore) UpsertUserConfig(_ context.Context, cfg identity.UserConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configs[cfg.UserID] = cfg
	return nil
}

func (s *fakeStore) GetProjectByOwner(_ context.Context, ownerUserID string) (identity.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[ownerUserID]
	if !ok {
		return identity.Project{}, identity.ErrNotFound
	}
	return p, nil
}

func (s *fakeStore) UpsertProject(_ context.Context, p identity.Project) (identity.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects[p.OwnerUserID] = p
	return p, nil
}

// touchSessionCallCount reports how many times TouchSession was invoked, for
// negative-renewal tests asserting a fresh session's expiry is left alone.
func (s *fakeStore) touchSessionCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.touchSessionCalls
}

// allSessions returns every session currently stored, for tests to assert
// against (e.g. that the raw token never appears, or that exactly one
// session survives) without needing the service's token-hash function.
func (s *fakeStore) allSessions() []identity.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]identity.Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, sess)
	}
	return out
}

// userCount reports how many users have been created, for tests asserting a
// rejected login created nothing.
func (s *fakeStore) userCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.users)
}

// fakeGitHub is an in-memory identity.GitHub double: it always returns user
// and token unless exchangeErr/fetchErr is set, and records the code/token it
// was last called with so tests can assert wiring.
type fakeGitHub struct {
	mu          sync.Mutex
	token       string
	user        githubapi.GitHubUser
	exchangeErr error
	fetchErr    error

	gotCode  string
	gotToken string
}

func (g *fakeGitHub) AuthorizeURL(state string) string {
	return "https://github.example/login/oauth/authorize?state=" + state
}

func (g *fakeGitHub) ExchangeCode(_ context.Context, code string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gotCode = code
	if g.exchangeErr != nil {
		return "", g.exchangeErr
	}
	return g.token, nil
}

func (g *fakeGitHub) FetchUser(_ context.Context, accessToken string) (githubapi.GitHubUser, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gotToken = accessToken
	if g.fetchErr != nil {
		return githubapi.GitHubUser{}, g.fetchErr
	}
	return g.user, nil
}

var _ identity.GitHub = (*fakeGitHub)(nil)

// fakeVerifier is an in-memory identity.Verifier double: it always reports
// "ok" and records the exact arguments each method was called with, so
// service tests can assert Verify decrypts secrets and resolves the repo URL
// before handing them to the verifier.
type fakeVerifier struct {
	mu sync.Mutex

	gotAnthropicKey string
	gotAmikaKey     string
	gotRepoURL      string
	gotRepoToken    string
}

func (v *fakeVerifier) VerifyAnthropic(_ context.Context, apiKey string) identity.CheckResult {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.gotAnthropicKey = apiKey
	return identity.CheckResult{Status: "ok"}
}

func (v *fakeVerifier) VerifyAmika(_ context.Context, apiKey string) identity.CheckResult {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.gotAmikaKey = apiKey
	return identity.CheckResult{Status: "ok"}
}

func (v *fakeVerifier) VerifyRepo(_ context.Context, repoURL, token string) identity.CheckResult {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.gotRepoURL = repoURL
	v.gotRepoToken = token
	return identity.CheckResult{Status: "ok"}
}

var _ identity.Verifier = (*fakeVerifier)(nil)
