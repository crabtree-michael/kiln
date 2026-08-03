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

// inviteOnlyPage is the small inline HTML shown when CompleteConnect rejects a
// GitHub login that isn't on the allowlist (11 §2) — a one-shot error page
// needs no template/asset dependency.
const inviteOnlyPage = `<!DOCTYPE html>
<html><head><title>Kiln</title></head>
<body><h1>Kiln is invite-only.</h1>
<p>Ask for your GitHub username to be added.</p></body></html>`

// repoScopeDeniedPage is shown when the callback comes back without full `repo`
// (CompleteConnect → ErrRepoScopeNotGranted). The caller IS signed in by then —
// GitHub authenticated an allowlisted account, it just withheld the repository
// permission — so the page says what is missing and offers both the one-click
// retry and the way into the dashboard. The alternative, a silent "Connected"
// card over a token that cannot clone or push, is far worse.
const repoScopeDeniedPage = `<!DOCTYPE html>
<html><head><title>Kiln</title></head>
<body><h1>GitHub didn't grant repository access.</h1>
<p>You're signed in, but Kiln needs the <code>repo</code> permission to clone your
repository, read it, and push the branches your agents produce. No repository
credential was saved.</p>
<p>If your repository belongs to an organisation, that organisation may need to
approve Kiln first.</p>
<p><a href="/auth/github/connect">Try again</a> &middot;
<a href="/dashboard">Back to settings</a></p></body></html>`

// handleAuthConnect starts THE GitHub dance (11 §2, amended 2026-08-03): mint a
// random state token, stash it in a short-lived HttpOnly cookie, and redirect
// the browser to GitHub's authorize URL carrying the same state (checked back
// at the callback as CSRF protection).
//
// There is exactly one grant now, and it always asks for `repo`. The scopeless
// sign-in that used to sit beside it is gone: two routes whose only difference
// was an invisible scope produced UI that pointed at the wrong one, and a
// "connected" account whose token couldn't clone. Signing in and connecting
// being the same act is also what keeps "Switch account" honest — re-running
// this flow always leaves the session and the stored token on the same GitHub
// account.
func (s *Server) handleAuthConnect(w http.ResponseWriter, r *http.Request) {
	token, err := randomToken()
	if err != nil {
		slog.Error("api: mint oauth state", "err", err)
		http.Error(w, "start login", http.StatusInternalServerError)
		return
	}
	setCookie(w, r, stateCookie, token, stateCookieTTL)
	http.Redirect(w, r, s.auth.ConnectURL(token), http.StatusFound)
}

// handleAuthCallback completes the GitHub OAuth dance (11 §2): the state cookie
// must match the query param exactly (constant-time comparison, CSRF defense),
// the completion enforces the allowlist and stores the repo credential, and
// success mints a session cookie and redirects into the app. The state cookie
// is cleared on every exit path, successful or not.
//
// With one grant there is nothing left to dispatch on — the old prefixed state
// that told this handler which of two completions to run went away with the
// second flow.
//
// A grant missing `repo` is the one partial outcome: the session is still
// minted (the account authenticated) and only the credential is refused, so the
// user lands signed-in on an explanation with a retry rather than bounced back
// out to a sign-in screen.
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

	user, err := s.auth.CompleteConnect(r.Context(), r.URL.Query().Get("code"))
	// Not a failure of Kiln's — the user (or their org) declined the repo
	// permission. The user comes back populated for exactly this case, so the
	// sign-in half still succeeds and only the credential is withheld.
	scopeDenied := errors.Is(err, identity.ErrRepoScopeNotGranted)
	switch {
	case errors.Is(err, identity.ErrNotAllowed):
		writeAuthPage(w, http.StatusForbidden, inviteOnlyPage, "invite-only")
		return
	case err != nil && !scopeDenied:
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
	if scopeDenied {
		// 403 with an explanation beats a 502 "github login failed" — and beats
		// dropping them on a dashboard that just says "not connected" with no
		// word on why the consent screen they just cleared didn't take.
		writeAuthPage(w, http.StatusForbidden, repoScopeDeniedPage, "repo-scope-denied")
		return
	}
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
