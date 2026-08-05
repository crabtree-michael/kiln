// Package githubmock is the offline stand-in for the GitHub API adapter,
// selected by KILN_GITHUB_MODE=mock (keyless e2e design §3, mirroring
// AGENT_MODE=mock / KILN_VOICE_MODE=mock / KILN_VERIFY_MODE=mock).
//
// It follows the same principle as the mock agent provider: fake ONLY the
// vendor. Sessions, the credential store, the repo-picker endpoint, and the
// dashboard form all run for real — only the calls that would reach github.com
// are canned. That is what lets the keyless lane exercise the real onboarding
// form, whose repo picker can no longer be typed into.
//
// It stands in for BOTH halves of the real adapter — the OAuth App flow and the
// GitHub App installation flow it is migrating to (design 2026-08-04) — so the
// keyless stack keeps booting and onboarding from either side of that move.
package githubmock

import (
	"context"
	"strconv"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity/githubapi"
)

// Repos is the canned listing every mock caller sees. Deliberately mixed
// public/private so the picker's private marker is exercised offline, and
// deliberately example.com-shaped so a keyless clone can never reach a real
// repository.
var Repos = []githubapi.Repo{
	{FullName: "keyless/demo", HTMLURL: "https://example.com/keyless/demo", Private: false},
	{FullName: "keyless/private-demo", HTMLURL: "https://example.com/keyless/private-demo", Private: true},
}

// Client is the offline identity.GitHub implementation.
type Client struct{}

// New builds the mock client.
func New() *Client { return &Client{} }

// AuthorizeURL returns a local, non-navigable placeholder: the keyless lane
// mints sessions through POST /api/dev/session, never the OAuth dance, so this
// exists only to satisfy the port. The scope rides in the query so a keyless
// run can still tell the sign-in grant from the connect grant.
func (c *Client) AuthorizeURL(state, scope string) string {
	u := "/auth/github/callback?state=" + state + "&code=mock-code"
	if scope != "" {
		u += "&scope=" + scope
	}
	return u
}

// ExchangeCode returns a canned access token and reports it as repo-scoped, so
// the offline connect grant lands a credential exactly as the real one does.
func (c *Client) ExchangeCode(context.Context, string) (string, string, error) {
	return MockToken, githubapi.ScopeRepo, nil
}

// TokenScopes reports the canned credential as repo-scoped, so a keyless run
// classifies as connected rather than sitting in the unknown-scopes state.
func (c *Client) TokenScopes(context.Context, string) (string, error) {
	return githubapi.ScopeRepo, nil
}

// FetchUser returns a canned profile without any network call.
func (c *Client) FetchUser(context.Context, string) (githubapi.GitHubUser, error) {
	return githubapi.GitHubUser{ID: 1, Login: "keyless-user", Name: "Keyless User"}, nil
}

// ListRepos returns the canned listing for any token — including the synthetic
// one a dev session stores, which is the whole point: a keyless user reaches
// the settings repo picker already "connected".
func (c *Client) ListRepos(context.Context, string) ([]githubapi.Repo, error) {
	return Repos, nil
}

// MockToken is the synthetic credential a keyless stack stores for its users.
// It is not a real token and cannot authenticate anywhere — it exists so the
// repo picker sees a "connected" account offline.
//
//nolint:gosec // G101: a deliberate, inert placeholder — this package IS the offline fake.
const MockToken = "mock-github-token"

// The GitHub App half (design 2026-08-04 §3), so a keyless stack keeps booting
// and onboarding through the migration. Same principle as the OAuth half above:
// fake ONLY the vendor. The installation id, the mint, and the narrowed listing
// are canned; everything they feed — the callback, the credential store, the
// picker — runs for real.

const (
	// MockInstallationID is the installation every keyless user "has". A fixed,
	// obviously-synthetic value: a keyless run must be able to assert on it, and
	// it must never collide with a real installation id.
	MockInstallationID = int64(4242)
	// MockInstallationToken is the canned mint result. Carries GitHub's `ghs_`
	// installation-token prefix so anything that reasons about token shape sees
	// the right thing offline, and is otherwise inert.
	//
	//nolint:gosec // G101: a deliberate, inert placeholder — this package IS the offline fake.
	MockInstallationToken = "ghs_mock-installation-token"
	// mockTokenTTL matches GitHub's real one-hour installation-token lifetime,
	// so a caller's refresh-before-expiry logic is exercised against a realistic
	// window offline rather than against an expiry that never arrives.
	mockTokenTTL = time.Hour
)

// InstallURL returns a local, non-navigable placeholder rather than a github.com
// install page — the keyless lane mints sessions through POST /api/dev/session
// and never leaves the stack.
//
// It carries `installation_id` and `setup_action` because those are exactly what
// the App-era callback must learn to read (design §3.2): a keyless run that
// follows this URL exercises the real parameter handling, which is the only part
// of the install flow Kiln owns.
func (c *Client) InstallURL(state string) string {
	return "/auth/github/callback?state=" + state +
		"&code=mock-code" +
		"&installation_id=" + strconv.FormatInt(MockInstallationID, 10) +
		"&setup_action=install"
}

// MintInstallationToken returns the canned installation credential for any
// installation id, with a real one-hour expiry so the caller's cache behaves as
// it will in production.
//
// RepositorySelection is "selected" rather than "all" on purpose: the narrowed
// choice is the state this whole migration exists to support, so it is the one a
// keyless run should exercise by default.
func (c *Client) MintInstallationToken(context.Context, int64) (githubapi.InstallationToken, error) {
	return githubapi.InstallationToken{
		Token:               MockInstallationToken,
		ExpiresAt:           time.Now().Add(mockTokenTTL),
		RepositorySelection: githubapi.RepositorySelectionSelected,
	}, nil
}

// ListInstallationRepos returns the same canned listing as ListRepos. The mock
// does NOT model a narrowed subset: which repos an installation covers is
// GitHub's state, and inventing an offline difference between the two listings
// would make the keyless lane assert on fiction rather than on Kiln's behaviour.
func (c *Client) ListInstallationRepos(context.Context, string, int64) ([]githubapi.Repo, error) {
	return Repos, nil
}
