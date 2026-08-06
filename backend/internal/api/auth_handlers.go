package api

// The GitHub App + cookie-session route handlers (11 §2): start the dance,
// complete it, and log out. Mounted only when EnableIdentity was called (see
// routes.go).

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
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

// installRequiredPage is shown when the callback authorized the USER but
// carried no installation (CompleteConnect → ErrInstallationRequired) — someone
// who cleared GitHub's sign-in screen and then backed out of the install itself.
// The caller IS signed in by then, so the page says what is missing and offers
// both the one-click retry and the way into the dashboard. The alternative, a
// silent "Connected" card over an account Kiln cannot read a single repository
// from, is far worse.
const installRequiredPage = `<!DOCTYPE html>
<html><head><title>Kiln</title></head>
<body><h1>Kiln wasn't installed on GitHub.</h1>
<p>You're signed in, but Kiln needs to be installed on your account or
organisation to reach your repositories. On the install screen you choose which
repositories it may touch — all of them, or just the ones you pick.</p>
<p>If the repository belongs to an organisation, an owner of that organisation
may need to approve the installation.</p>
<p><a href="/auth/github/connect">Try again</a> &middot;
<a href="/dashboard">Back to settings</a></p></body></html>`

// handleAuthConnect starts THE GitHub dance (11 §2, amended 2026-08-03 and by
// the GitHub App migration): mint a random state token, stash it in a
// short-lived HttpOnly cookie, and redirect the browser to GitHub's install URL
// carrying the same state (checked back at the callback as CSRF protection).
//
// There is exactly one flow, and it now ends on GitHub's repository chooser
// rather than an all-or-nothing `repo` consent screen. The scopeless sign-in
// that used to sit beside it is long gone: two routes whose only difference was
// an invisible scope produced UI that pointed at the wrong one. Signing in and
// connecting being the same act is also what keeps "Switch account" honest —
// re-running this flow always leaves the session and the installation on the
// same GitHub account.
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

// handleAuthCallback completes the GitHub App dance (11 §2): the state cookie
// must match the query param exactly (constant-time comparison, CSRF defense),
// the completion enforces the allowlist and records the installation, and
// success mints a session cookie and redirects into the app. The state cookie
// is cleared on every exit path, successful or not.
//
// GitHub delivers this callback in two shapes, and both are ordinary (design
// §3.2). Coming through Kiln's own connect link, `code` and `installation_id`
// arrive together and this is the full identity path. Coming from GitHub's own
// Apps page, only `installation_id` arrives — there was no authorize step to
// produce a code — and the installation is attached to whoever is already
// signed in. Neither present is the only real error, and it means the user
// should start at sign-in.
//
// An install the user backed out of is the one partial outcome: the session is
// still minted (the account authenticated) and only the repository half is
// refused, so they land signed-in on an explanation with a retry rather than
// bounced back out to a sign-in screen.
func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	// Set eagerly, before any WriteHeader below: once the status line is
	// written the header map is flushed, so a deferred clear would silently
	// never reach the client.
	clearCookie(w, r, stateCookie)

	q := r.URL.Query()
	code := q.Get("code")
	installationID := parseInstallationID(q.Get("installation_id"))

	// The state check guards the code exchange, which is the half that can be
	// replayed against a victim's browser. A bare installation callback from
	// GitHub's Apps page carries no state cookie (the browser never passed
	// through /auth/github/connect), so it is authenticated by the session it
	// already has instead — see handleBareInstall.
	if code == "" {
		s.handleBareInstall(w, r, installationID)
		return
	}

	c, err := r.Cookie(stateCookie)
	if err != nil {
		http.Error(w, "missing oauth state cookie", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(c.Value), []byte(q.Get("state"))) != 1 {
		http.Error(w, "oauth state mismatch", http.StatusBadRequest)
		return
	}

	user, err := s.auth.CompleteConnect(r.Context(), code, installationID)
	// Not a failure of Kiln's — the user cleared GitHub's sign-in screen and
	// then declined the install. The user comes back populated for exactly this
	// case, so the sign-in half still succeeds and only the repositories are
	// withheld.
	installMissing := errors.Is(err, identity.ErrInstallationRequired)
	switch {
	case errors.Is(err, identity.ErrNotAllowed):
		writeAuthPage(w, http.StatusForbidden, inviteOnlyPage, "invite-only")
		return
	case err != nil && !installMissing:
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
	if installMissing {
		// 403 with an explanation beats a 502 "github login failed" — and beats
		// dropping them on a dashboard that just says "not connected" with no
		// word on why the screen they just cleared didn't take.
		writeAuthPage(w, http.StatusForbidden, installRequiredPage, "install-required")
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

// handleBareInstall handles the code-less callback: someone installed Kiln from
// GitHub's own Apps or Marketplace page, so GitHub sent them here with an
// installation and nothing to exchange.
//
// It authenticates on the existing session rather than on state, because there
// is no state to check — the browser never passed through /auth/github/connect.
// Without a session there is nobody to attach the installation to, so the only
// honest answer is to send them through the front door, where the normal flow
// will pick the very same installation back up.
func (s *Server) handleBareInstall(w http.ResponseWriter, r *http.Request, installationID int64) {
	if installationID == 0 {
		http.Error(w, "missing code and installation_id", http.StatusBadRequest)
		return
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		http.Redirect(w, r, "/auth/github/connect", http.StatusFound)
		return
	}
	user, _, err := s.auth.ResolveSession(r.Context(), c.Value)
	if err != nil {
		http.Redirect(w, r, "/auth/github/connect", http.StatusFound)
		return
	}
	if err := s.auth.AttachInstallation(r.Context(), user.ID, installationID); err != nil {
		slog.Error("api: attach github installation", "err", err)
		http.Error(w, "github install failed", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

// parseInstallationID reads GitHub's `installation_id` query parameter, mapping
// anything absent or unparseable to 0 — the "no installation" value every layer
// below already treats as not-connected. A malformed value is GitHub's problem
// to have sent, not a reason to 500 mid-sign-in.
func parseInstallationID(raw string) int64 {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		return 0
	}
	return id
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
