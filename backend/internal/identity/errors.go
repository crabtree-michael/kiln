package identity

import "errors"

var (
	// ErrNotFound is returned by stores when the row does not exist.
	ErrNotFound = errors.New("identity: not found")
	// ErrNotAllowed rejects a GitHub login not on KILN_ALLOWED_GITHUB_USERS (11 §2).
	ErrNotAllowed = errors.New("identity: github user not on the allowlist")
	// ErrNoSession rejects a missing/expired/unknown session token.
	ErrNoSession = errors.New("identity: no valid session")
)
