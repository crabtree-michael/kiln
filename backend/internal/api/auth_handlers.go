package api

// The GitHub OAuth + cookie-session route handlers (11 §2): start the
// dance, complete it, and log out. Mounted only when EnableIdentity was
// called (see routes.go).

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// stateCookieTTL bounds how long an in-flight OAuth state token is valid
// (11 §2): long enough for a user to complete GitHub's consent screen, short
// enough to bound a CSRF replay window.
const stateCookieTTL = 10 * time.Minute

// inviteOnlyPage is the small inline HTML shown when CompleteLogin rejects a
// GitHub login that isn't on the allowlist (11 §2) — a one-shot error page
// needs no template/asset dependency.
const inviteOnlyPage = `<!DOCTYPE html>
<html><head><title>Kiln</title></head>
<body><h1>Kiln is invite-only.</h1>
<p>Ask for your GitHub username to be added.</p></body></html>`

// handleAuthLogin starts the GitHub OAuth dance (11 §2): mint a random state
// token, stash it in a short-lived HttpOnly cookie, and redirect the browser
// to GitHub's authorize URL carrying the same state (checked back at the
// callback as CSRF protection).
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	state, err := randomToken()
	if err != nil {
		slog.Error("api: mint oauth state", "err", err)
		http.Error(w, "start login", http.StatusInternalServerError)
		return
	}
	setCookie(w, r, stateCookie, state, stateCookieTTL)
	http.Redirect(w, r, s.auth.LoginURL(state), http.StatusFound)
}

// handleAuthCallback completes the GitHub OAuth dance (11 §2): the state
// cookie must match the query param exactly (constant-time comparison, CSRF
// defense), CompleteLogin enforces the allowlist, and success mints a
// session cookie and redirects into the app. The state cookie is cleared on
// every exit path, successful or not.
func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	// Set eagerly, before any WriteHeader below: once the status line is
	// written the header map is flushed, so a deferred clear would silently
	// never reach the client.
	clearCookie(w, r, stateCookie)

	c, err := r.Cookie(stateCookie)
	if err != nil {
		http.Error(w, "missing oauth state cookie", http.StatusBadRequest)
		return
	}
	wantState := r.URL.Query().Get("state")
	if subtle.ConstantTimeCompare([]byte(c.Value), []byte(wantState)) != 1 {
		http.Error(w, "oauth state mismatch", http.StatusBadRequest)
		return
	}

	user, err := s.auth.CompleteLogin(r.Context(), r.URL.Query().Get("code"))
	switch {
	case errors.Is(err, identity.ErrNotAllowed):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		if _, werr := w.Write([]byte(inviteOnlyPage)); werr != nil {
			slog.Error("api: write invite-only page", "err", werr)
		}
		return
	case err != nil:
		slog.Error("api: complete github login", "err", err)
		http.Error(w, "github login failed", http.StatusBadGateway)
		return
	}

	token, expires, err := s.auth.CreateSession(r.Context(), user.ID)
	if err != nil {
		slog.Error("api: create session", "err", err)
		http.Error(w, "create session", http.StatusInternalServerError)
		return
	}
	setCookie(w, r, sessionCookie, token, time.Until(expires))
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

// handleLogout clears the caller's session (11 §2): idempotent — always
// 204, whether or not a session cookie was present, so a client can call it
// unconditionally on sign-out.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.auth.Logout(r.Context(), c.Value); err != nil {
			slog.Error("api: logout", "err", err)
		}
	}
	clearCookie(w, r, sessionCookie)
	w.WriteHeader(http.StatusNoContent)
}
