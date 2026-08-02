package api

// The GitHub OAuth + cookie-session route handlers (11 §2): start the
// dance, complete it, and log out. Mounted only when EnableIdentity was
// called (see routes.go).

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"
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

// repoScopeDeniedPage is shown when the "Connect GitHub" callback comes back
// without full `repo` (CompleteConnect → ErrRepoScopeNotGranted). Kiln stores
// nothing in that case, so the page has to say plainly what was missing and
// offer the one-click retry — the alternative, a silent "Connected" card over a
// token that cannot clone or push, is far worse.
const repoScopeDeniedPage = `<!DOCTYPE html>
<html><head><title>Kiln</title></head>
<body><h1>GitHub didn't grant repository access.</h1>
<p>Kiln needs the <code>repo</code> permission to clone your repository, read it,
and push the branches your agents produce. Nothing was saved.</p>
<p>If your repository belongs to an organisation, that organisation may need to
approve Kiln first.</p>
<p><a href="/auth/github/connect">Try connecting again</a> &middot;
<a href="/dashboard">Back to settings</a></p></body></html>`

// connectStatePrefix marks an in-flight OAuth state as belonging to the
// repo-scoped CONNECT grant rather than a plain sign-in. GitHub sends both
// grants back to the OAuth app's single registered callback URL, so the
// callback has to be told which one it is completing; carrying the intent on
// the state itself means no second cookie to set, match, and clear.
//
// It is read back off the SERVER-SET HttpOnly state cookie, never off the
// query param, so a caller cannot talk the callback into the connect path (and
// into storing a token) by hand-crafting a URL. The prefix rides along on both
// halves, so the existing exact-match CSRF check covers it unchanged.
const connectStatePrefix = "connect:"

// handleAuthLogin starts the plain sign-in dance (11 §2): mint a random state
// token, stash it in a short-lived HttpOnly cookie, and redirect the browser
// to GitHub's authorize URL carrying the same state (checked back at the
// callback as CSRF protection). No scopes — identity only.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	s.startOAuth(w, r, "", s.auth.LoginURL)
}

// handleAuthConnect starts the repo-scoped grant behind the dashboard's
// "Connect GitHub" card. Identical to handleAuthLogin except that it asks
// GitHub for `repo` access and marks the state so the shared callback stores
// the resulting token as the caller's repo credential. Connecting also signs
// the authorizing account in, which is what makes "Switch account" work: it
// re-runs this same flow, so the session and the stored token always belong to
// the same GitHub account.
func (s *Server) handleAuthConnect(w http.ResponseWriter, r *http.Request) {
	s.startOAuth(w, r, connectStatePrefix, s.auth.ConnectURL)
}

// startOAuth is the shared opening move of both grants: mint state (prefixed
// for connect), stash it, redirect to the grant's authorize URL.
func (s *Server) startOAuth(
	w http.ResponseWriter, r *http.Request, prefix string, authorizeURL func(string) string,
) {
	token, err := randomToken()
	if err != nil {
		slog.Error("api: mint oauth state", "err", err)
		http.Error(w, "start login", http.StatusInternalServerError)
		return
	}
	state := prefix + token
	setCookie(w, r, stateCookie, state, stateCookieTTL)
	http.Redirect(w, r, authorizeURL(state), http.StatusFound)
}

// handleAuthCallback completes the GitHub OAuth dance for BOTH grants (11 §2):
// the state cookie must match the query param exactly (constant-time
// comparison, CSRF defense), the completion enforces the allowlist, and
// success mints a session cookie and redirects into the app. The state
// cookie's own prefix selects the completion: a plain sign-in discards the
// access token, a connect stores it as the caller's repo credential. The state
// cookie is cleared on every exit path, successful or not.
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

	// The intent comes off the cookie we set, not the query param — see
	// connectStatePrefix. By here the two have already been proven equal.
	complete := s.auth.CompleteLogin
	if strings.HasPrefix(c.Value, connectStatePrefix) {
		complete = s.auth.CompleteConnect
	}

	user, err := complete(r.Context(), r.URL.Query().Get("code"))
	switch {
	case errors.Is(err, identity.ErrNotAllowed):
		writeAuthPage(w, http.StatusForbidden, inviteOnlyPage, "invite-only")
		return
	case errors.Is(err, identity.ErrRepoScopeNotGranted):
		// Not a failure of Kiln's — the user (or their org) declined the grant.
		// 403 with an explanation beats a 502 "github login failed".
		writeAuthPage(w, http.StatusForbidden, repoScopeDeniedPage, "repo-scope-denied")
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

// writeAuthPage renders one of the callback's terminal HTML explanations
// (invite-only, repo-scope-denied) with its status. `what` only names the page
// in the log line if the write itself fails.
func writeAuthPage(w http.ResponseWriter, status int, page, what string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write([]byte(page)); err != nil {
		slog.Error("api: write auth page", "page", what, "err", err)
	}
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
