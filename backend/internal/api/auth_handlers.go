package api

// The GitHub App + cookie-session route handlers (11 §2): start the dance,
// complete it, and log out. Mounted only when EnableIdentity was called (see
// routes.go).

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// stateCookieTTL bounds how long an in-flight OAuth state token is valid
// (11 §2): long enough for a user to complete GitHub's consent screen, short
// enough to bound a CSRF replay window.
const stateCookieTTL = 10 * time.Minute

// The two places a completed sign-in can land: the primary screen (08), and the
// settings screen that owns onboarding and the connection cards (11 §5).
const (
	appPath       = "/app"
	dashboardPath = "/dashboard"
)

// privateBetaPath is where a GitHub identity that is NOT on the allowlist ends
// up: the SPA's public "you're on the list, we'll be in touch" screen. It is a
// third landing beside the two above, and deliberately not one of them — the
// caller has no session and no user row, so both would bounce them straight back
// to a sign-in they cannot complete.
const privateBetaPath = "/beta/pending"

// nextParam is how a caller asks for a landing other than the default, and
// dashboardDest is its one accepted value. A closed set rather than a path,
// deliberately: a `next` that carried a URL would be an open redirect hanging
// off the one route an attacker most wants to aim, for the sake of a choice
// between two destinations this package already knows by name.
const (
	nextParam     = "next"
	dashboardDest = "dashboard"
)

// dashboardStateSuffix marks an in-flight sign-in that must come back to the
// settings screen. It rides inside the state nonce — see mintAuthState.
const dashboardStateSuffix = ".dashboard"

// (The allowlist rejection used to serve a one-shot inline "Kiln is invite-only —
// ask for your GitHub username to be added" page from here. It now records the
// login and redirects to privateBetaPath, so no one has to go and find someone
// to ask: see rejectToPrivateBeta.)

// installRequiredPage is the end of the second leg for someone who declined it:
// they authorized, were sent to GitHub's install screen, and came back with no
// installation again. The caller IS signed in by then, so the page says what is
// missing and offers both the one-click retry and the way into the dashboard.
// The alternative, a silent "Connected" card over an account Kiln cannot read a
// single repository from, is far worse.
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

// handleAuthConnect starts THE GitHub dance (11 §2, amended 2026-08-03, by the
// GitHub App migration, and again 2026-08-06): mint a random state token, stash
// it in a short-lived HttpOnly cookie, and redirect the browser to GitHub
// carrying the same state (checked back at the callback as CSRF protection).
//
// There is exactly one flow and one route. The scopeless sign-in that used to
// sit beside it is long gone: two routes whose only difference was an invisible
// scope produced UI that pointed at the wrong one.
//
// `?setup=1` is the one variation, and it survives that history because it is
// nothing like the trap: it asks for GitHub's repository chooser explicitly, and
// both ways of getting it wrong are harmless. Asking for setup without an
// installation installs and returns; omitting it without an installation lands
// on the chooser anyway, because the callback sends them there. It exists
// because a CONNECTED user asking to change accounts or repositories wants the
// screen the plain route deliberately skips — GitHub's configure page, which for
// them is the destination rather than the dead end it was as a sign-in target.
//
// `?next=dashboard` is the other, and it names where the flow ENDS rather than
// where it goes: the affordances on the settings screen send the user back to
// the settings screen, and everything else lands them in the app (postAuthPath).
// `setup=1` implies it, being asked for from that screen and nowhere else.
func (s *Server) handleAuthConnect(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	setup := q.Get("setup") == "1"
	token, err := mintAuthState(setup || q.Get(nextParam) == dashboardDest)
	if err != nil {
		slog.Error("api: mint oauth state", "err", err)
		http.Error(w, "start login", http.StatusInternalServerError)
		return
	}
	setCookie(w, r, stateCookie, token, stateCookieTTL)
	target := s.auth.ConnectURL(token)
	if setup {
		target = s.auth.InstallURL(token)
	}
	// Never served from a cache. This response pairs one freshly minted nonce
	// with the one URL carrying it, so a browser that replayed an earlier one
	// would send the user to a stale entry point holding a nonce the callback
	// rejects — and the entry point is exactly what the last two fixes changed.
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusFound)
}

// mintAuthState mints the CSRF nonce for one trip to GitHub, carrying the
// destination the browser should come back to.
//
// The destination rides IN the state rather than in a cookie of its own because
// it has to survive the same round trip, and state already does: GitHub echoes
// it back verbatim, and the callback has proved it matches the cookie before
// anything reads it. A second cookie would be another thing to set, clear and
// re-mint on the install hop, guarded by nothing.
//
// randomToken is base64url, whose alphabet has no ".", so the suffix can never
// be mistaken for part of the nonce it is appended to.
func mintAuthState(toDashboard bool) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	if toDashboard {
		return token + dashboardStateSuffix, nil
	}
	return token, nil
}

// stateWantsDashboard reads the destination back out of a state value. Call it
// only on a state that has already passed the callback's comparison against the
// cookie: this is the one part of that string acted on, and an unverified one is
// whatever the caller felt like sending.
func stateWantsDashboard(state string) bool {
	return strings.HasSuffix(state, dashboardStateSuffix)
}

// handleAuthCallback completes the GitHub App dance (11 §2): the state cookie
// must match the query param exactly (constant-time comparison, CSRF defense),
// the completion enforces the allowlist and records the installation, and
// success mints a session cookie and redirects into the app. The state cookie
// is cleared on every exit path, successful or not.
//
// GitHub delivers this callback in two shapes, and both are ordinary (design
// §3.2). Coming through Kiln's own connect link, `code` arrives — with
// `installation_id` beside it only when the installation was created on that
// same trip — and this is the full identity path. Coming from GitHub's own Apps
// page, only `installation_id` arrives, there having been no authorize step to
// produce a code, and the installation is attached to whoever is already signed
// in. Neither present is the only real error, and it means the user should start
// at sign-in.
//
// A user with nothing installed is the one partial outcome, and it is a hop
// rather than a failure: the session is minted (the account authenticated) and
// the browser goes on to GitHub's repository chooser, which is the half still
// missing. Exactly one hop — coming back from that screen still installation-less
// means the user declined it, and the honest answer then is the page saying so.
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
	// Not a failure of Kiln's — this account has authorized Kiln and installed
	// it nowhere. The user comes back populated for exactly this case, so the
	// sign-in half still succeeds and only the repositories are outstanding.
	installMissing := errors.Is(err, identity.ErrInstallationRequired)
	switch {
	case errors.Is(err, identity.ErrNotAllowed):
		// Is for the branch, As for the payload: the branch is owed to every
		// allowlist rejection, while the login rides only on the typed error.
		// Matching solely with As would send a bare ErrNotAllowed down the 502
		// path — a user told "github login failed" for being early rather than
		// wrong.
		var notAllowed *identity.NotAllowedError
		login := ""
		if errors.As(err, &notAllowed) {
			login = notAllowed.Login
		}
		s.rejectToPrivateBeta(w, r, login)
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
	// c.Value, not the query parameter: the two were just proved equal, and the
	// cookie is the half this server wrote.
	toDashboard := stateWantsDashboard(c.Value)
	if installMissing {
		s.sendToInstall(w, r, toDashboard)
		return
	}
	clearCookie(w, r, installPromptCookie)
	http.Redirect(w, r, s.postAuthPath(r.Context(), user.ID, toDashboard), http.StatusFound)
}

// postAuthPath is where a completed sign-in lands, and the answer is normally
// the app. Signing in is how a person gets INTO Kiln, and a browser parked on
// the settings screen has stopped one step short of the board it came for.
//
// It read as a phone-works/laptop-doesn't bug because on a phone it is invisible:
// an installed web app relaunches at its start_url and DefaultRoute sends it to
// /app, so the settings screen the callback chose never survives long enough to
// be seen. A browser tab just stays on it — which, after the entry point was
// fixed to stop stranding returning users on github.com, was the whole of what
// was left of "sign in and end up on a settings page".
//
// Two users still belong there. One has no project, so the app has nothing to
// show them and onboarding is what they need. The other STARTED there — the
// connection cards and the chooser trip ask for `next=dashboard` — and wants to
// go back to what they were configuring; the app would be a different way of
// losing their place.
//
// A listing that fails resolves to the settings screen, the destination that is
// merely a detour for one of those users and is a blank board for the other.
func (s *Server) postAuthPath(ctx context.Context, userID string, toDashboard bool) string {
	if toDashboard || s.account == nil {
		return dashboardPath
	}
	projects, err := s.account.ListProjects(ctx, userID)
	if err != nil {
		slog.Error("api: list projects for sign-in landing", "user_id", userID, "err", err)
		return dashboardPath
	}
	if len(projects) == 0 {
		return dashboardPath
	}
	return appPath
}

// sendToInstall runs the flow's second leg for a signed-in user with no
// installation: on to GitHub's repository chooser, which is the only screen that
// can grant the half they are missing.
//
// Once. The install screen is the one place in this flow a user can decline, and
// a decline looks identical to never having been there — same callback, same
// missing installation — so without the marker cookie the two would be
// indistinguishable and the browser would bounce between Kiln and GitHub for as
// long as the user kept saying no. With it, the second pass through here stops
// on the page that explains what is missing and offers the retry deliberately.
//
// The state cookie is re-minted for the trip. It was cleared eagerly at the top
// of the callback, and this Set-Cookie is written after that clear for the same
// name and path, so the browser keeps this one — the install screen's own
// callback then has state to check, exactly as the authorize screen's did. It
// is re-minted carrying the same destination, so a user who set out from the
// settings screen still ends there after the detour.
func (s *Server) sendToInstall(w http.ResponseWriter, r *http.Request, toDashboard bool) {
	if _, err := r.Cookie(installPromptCookie); err == nil {
		clearCookie(w, r, installPromptCookie)
		// 403 with an explanation beats a 502 "github login failed" — and beats
		// dropping them on a dashboard that just says "not connected" with no
		// word on why the screen they just cleared didn't take.
		writeAuthPage(w, http.StatusForbidden, installRequiredPage, "install-required")
		return
	}
	state, err := mintAuthState(toDashboard)
	if err != nil {
		slog.Error("api: mint install state", "err", err)
		writeAuthPage(w, http.StatusForbidden, installRequiredPage, "install-required")
		return
	}
	setCookie(w, r, stateCookie, state, stateCookieTTL)
	setCookie(w, r, installPromptCookie, "1", stateCookieTTL)
	http.Redirect(w, r, s.auth.InstallURL(state), http.StatusFound)
}

// rejectToPrivateBeta ends the flow for a GitHub identity that authenticated
// fine and simply isn't admitted yet (11 §2 allowlist). Two things happen, in
// this order:
//
//  1. The login is recorded on the private-beta list, so wanting in is a fact we
//     hold rather than something the user has to go and tell someone. This is
//     the only moment it can be captured — no user row exists for a rejected
//     login, and nothing about them survives this request otherwise.
//  2. The browser is redirected to the SPA's private-beta screen, which says the
//     product is in private beta and that we will be in touch.
//
// A failed record is logged and swallowed on purpose. The screen is the user's
// half of this and it reads the same either way; failing the sign-in over a
// bookkeeping write would replace "we'll be in touch" with a 500 for someone who
// did nothing wrong. The registrar is idempotent on the login, so a retried
// sign-in retries the record too — the write gets more than one chance.
//
// An empty login (a rejection that arrived without one) records nothing and
// still shows the screen: there is no identity to hold, and a blank row would be
// a record of nobody.
func (s *Server) rejectToPrivateBeta(w http.ResponseWriter, r *http.Request, login string) {
	if s.beta != nil && login != "" {
		if err := s.beta.Register(r.Context(), login); err != nil {
			slog.Error("api: record private-beta request", "login", login, "err", err)
		}
	}
	http.Redirect(w, r, privateBetaPath, http.StatusFound)
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
	// The install this browser was sent for has landed, by the other callback
	// shape — clear the one-hop marker so a later sign-in starts fresh.
	clearCookie(w, r, installPromptCookie)
	// No state came with this shape, so there is no stated destination to honour
	// and the default applies: into the app, or onboarding for a user who has
	// installed Kiln on GitHub before making a project.
	http.Redirect(w, r, s.postAuthPath(r.Context(), user.ID, false), http.StatusFound)
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
