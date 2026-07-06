package api

// Cookie and session-token plumbing behind the GitHub OAuth + cookie-session
// routes (11 §2): the two cookie names, a CSPRNG token minter shared by the
// OAuth state and (inside identity) the session token itself, the
// withSession guard Task 9/10's protected routes wrap with, and the
// setCookie/clearCookie helpers every cookie write goes through so the
// HttpOnly/SameSite/Secure flags never drift between call sites.

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// sessionCookie names the HttpOnly cookie carrying the caller's session
// token (11 §2).
const sessionCookie = "kiln_session"

// stateCookie names the HttpOnly cookie carrying the in-flight OAuth CSRF
// state token (11 §2); cleared on every /auth/github/callback exit path.
const stateCookie = "kiln_oauth_state"

// randomTokenBytes is the CSPRNG entropy behind a minted OAuth state token,
// before base64url encoding.
const randomTokenBytes = 32

// withSession authenticates the request via the kiln_session cookie and
// hands the resolved user to the wrapped handler; 401 otherwise. Phase 1
// guards ONLY the new identity-aware routes (11 §2, added by Task 9/10) —
// existing handlers are never wrapped. On a successful resolve it re-issues
// the session cookie against the (possibly slid) expiry ResolveSession
// returns, so the browser's cookie lifetime tracks the DB's sliding window
// instead of expiring ~30 days after the original login regardless of
// activity (final review, Important #1).
func (s *Server) withSession(next func(http.ResponseWriter, *http.Request, identity.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		user, expiresAt, err := s.auth.ResolveSession(r.Context(), c.Value)
		if err != nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		setCookie(w, r, sessionCookie, c.Value, time.Until(expiresAt))
		next(w, r, user)
	}
}

// randomToken mints a CSPRNG token suitable for an OAuth state parameter:
// randomTokenBytes of entropy, base64url-encoded (no padding) so it drops
// cleanly into both a cookie value and a URL query param.
func randomToken() (string, error) {
	raw := make([]byte, randomTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("api: mint random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// setCookie writes a cookie scoped to the whole app (Path=/): HttpOnly and
// SameSite=Lax so it never rides a cross-site request, Secure whenever the
// request itself arrived over TLS or behind a TLS-terminating proxy
// (X-Forwarded-Proto, Render's ingress in prod), expiring after maxAge.
func setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge time.Duration) {
	//nolint:gosec // G124: HttpOnly/SameSite are literal true/Lax; Secure is
	// genuinely conditional on requestIsTLS(r) (11 §2 — plain HTTP in local
	// dev, Secure once TLS-terminated in prod), which gosec can't prove statically.
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   requestIsTLS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// clearCookie expires a cookie previously written by setCookie (same scope
// and flags), so a logout or a spent OAuth state cookie disappears from the
// browser.
func clearCookie(w http.ResponseWriter, r *http.Request, name string) {
	//nolint:gosec // G124: see setCookie — Secure is conditional on requestIsTLS(r), not a static literal.
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   requestIsTLS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// requestIsTLS reports whether the request should be treated as arriving
// over TLS — directly, or terminated by a reverse proxy that sets
// X-Forwarded-Proto (Render's ingress in prod) — so setCookie/clearCookie
// mark a cookie Secure only when that's actually true.
func requestIsTLS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
