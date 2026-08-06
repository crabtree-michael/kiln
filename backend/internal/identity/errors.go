package identity

import "errors"

var (
	// ErrNotFound is returned by stores when the row does not exist.
	ErrNotFound = errors.New("identity: not found")
	// ErrNotAllowed rejects a GitHub login not on KILN_ALLOWED_GITHUB_USERS (11 §2).
	ErrNotAllowed = errors.New("identity: github user not on the allowlist")
	// ErrNoSession rejects a missing/expired/unknown session token.
	ErrNoSession = errors.New("identity: no valid session")
	// ErrInvalidProject rejects a project write missing required fields or
	// with a worker count outside the DB's 1-10 CHECK constraint.
	ErrInvalidProject = errors.New("identity: project needs a name, a repo_url, and worker_count 1-10")
	// ErrGitHubNotConnected reports that the caller has no usable GitHub
	// credential for repo access: they have never installed the App, or the
	// installation they had was rejected. The fix is always the same — run the
	// "Connect GitHub" flow — so the api maps this to a "not connected"
	// listing rather than an error.
	ErrGitHubNotConnected = errors.New("identity: no authorized github credential")
	// ErrInstallationRequired reports that the authorizing account has installed
	// the App NOWHERE — not that the callback merely omitted an installation id,
	// which is the ordinary shape of every sign-in past the first. It is the
	// GitHub App equivalent of a grant that authenticates you and gives Kiln no
	// repository access. Returned ALONGSIDE a populated user (the account really
	// did authenticate), so the caller can sign them in and send them on to the
	// install screen for the half still missing.
	ErrInstallationRequired = errors.New("identity: github app was not installed")
)
