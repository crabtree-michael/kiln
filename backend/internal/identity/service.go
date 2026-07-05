package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strings"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity/githubapi"
)

const (
	sessionTTL        = 30 * 24 * time.Hour
	sessionRenewBelow = 15 * 24 * time.Hour

	// sessionTokenBytes is the amount of CSPRNG entropy in a raw session
	// token before base64url encoding.
	sessionTokenBytes = 32

	// maxPositiveInt63 masks off the sign bit so a DevSignIn-derived GitHub
	// id (an fnv64a hash) always fits int64 as a positive value.
	maxPositiveInt63 = 0x7fffffffffffffff
)

// GitHub is the service's port onto the OAuth provider — satisfied directly
// by *githubapi.Client (the consumer declares the interface, 02 §2).
type GitHub interface {
	AuthorizeURL(state string) string
	ExchangeCode(ctx context.Context, code string) (string, error)
	FetchUser(ctx context.Context, accessToken string) (githubapi.GitHubUser, error)
}

// Service is identity's domain service (11 §2–§4): login, sessions, config.
type Service struct {
	store   Store
	cipher  *Cipher
	gh      GitHub
	allowed map[string]bool
	now     func() time.Time
}

func NewService(store Store, cipher *Cipher, gh GitHub, allowedLogins []string) *Service {
	allowed := make(map[string]bool, len(allowedLogins))
	for _, l := range allowedLogins {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
			allowed[l] = true
		}
	}
	return &Service{store: store, cipher: cipher, gh: gh, allowed: allowed, now: time.Now}
}

func (s *Service) LoginURL(state string) string { return s.gh.AuthorizeURL(state) }

// CompleteLogin exchanges the OAuth code, enforces the allowlist on every
// login (11 §2), and finds-or-creates the user.
func (s *Service) CompleteLogin(ctx context.Context, code string) (User, error) {
	token, err := s.gh.ExchangeCode(ctx, code)
	if err != nil {
		return User{}, fmt.Errorf("identity: complete login: %w", err)
	}
	ghUser, err := s.gh.FetchUser(ctx, token)
	if err != nil {
		return User{}, fmt.Errorf("identity: complete login: %w", err)
	}
	login := strings.ToLower(ghUser.Login)
	if !s.allowed[login] {
		return User{}, ErrNotAllowed
	}
	return s.upsertFromGitHub(ctx, ghUser)
}

// DevSignIn is the KILN_DEV_ENDPOINTS-only seam (11 §7): find-or-create with
// NO allowlist check, so e2e can mint sessions without real OAuth.
func (s *Service) DevSignIn(ctx context.Context, login string) (User, error) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.ToLower(login)))
	return s.upsertFromGitHub(ctx, githubapi.GitHubUser{
		ID:    int64(h.Sum64() & maxPositiveInt63), // deterministic dev id, not crypto
		Login: login,
		Name:  login,
	})
}

func (s *Service) CreateSession(ctx context.Context, userID string) (string, time.Time, error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("identity: session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := s.now().Add(sessionTTL)
	err := s.store.InsertSession(ctx, Session{TokenHash: hashToken(token), UserID: userID, ExpiresAt: expires})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("identity: create session: %w", err)
	}
	return token, expires, nil
}

// ResolveSession authenticates a request: unknown/expired ⇒ ErrNoSession;
// under half the TTL remaining ⇒ slide the window (11 §2).
func (s *Service) ResolveSession(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrNoSession
	}
	sess, user, err := s.store.GetSessionUser(ctx, hashToken(token))
	if err != nil {
		return User{}, ErrNoSession
	}
	now := s.now()
	if now.After(sess.ExpiresAt) {
		if derr := s.store.DeleteSession(ctx, sess.TokenHash); derr != nil {
			slog.ErrorContext(ctx, "identity: delete expired session", "err", derr)
		}
		return User{}, ErrNoSession
	}
	if sess.ExpiresAt.Sub(now) < sessionRenewBelow {
		if err := s.store.TouchSession(ctx, sess.TokenHash, now.Add(sessionTTL)); err != nil {
			return User{}, fmt.Errorf("identity: touch session: %w", err)
		}
	}
	return user, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := s.store.DeleteSession(ctx, hashToken(token)); err != nil {
		return fmt.Errorf("identity: logout: %w", err)
	}
	return nil
}

func (s *Service) upsertFromGitHub(ctx context.Context, gh githubapi.GitHubUser) (User, error) {
	u, err := s.store.UpsertUser(ctx, User{
		GitHubID:    gh.ID,
		GitHubLogin: strings.ToLower(gh.Login),
		DisplayName: gh.Name,
		AvatarURL:   gh.AvatarURL,
	})
	if err != nil {
		return User{}, fmt.Errorf("identity: upsert user: %w", err)
	}
	return u, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
