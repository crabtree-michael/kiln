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
// It stands in for both halves of the real adapter — the GitHub App's
// user-authorization flow and its installation flow (design 2026-08-04) — so a
// keyless stack onboards through the same code path production does.
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

// ExchangeCode returns a canned USER access token, so the offline flow lands a
// credential exactly where the real one does.
func (c *Client) ExchangeCode(context.Context, string) (string, error) {
	return MockToken, nil
}

// ConfigureURL returns a local, non-navigable stand-in for GitHub's
// installation-settings page: the link must render (the Integrations card shows
// it beside Connected) but must never send a keyless run to github.com.
func (c *Client) ConfigureURL(installationID int64) string {
	if installationID == 0 {
		return ""
	}
	return "/mock/github/installations/" + strconv.FormatInt(installationID, 10)
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

// AuthorizeURL returns the same local placeholder WITHOUT an installation_id —
// which is the whole point of the shape. GitHub's authorize screen names no
// installation, so a keyless run through the sign-in route exercises the
// resolution path a returning user takes (ListUserInstallations) rather than the
// first-visit one where the callback hands the id over.
func (c *Client) AuthorizeURL(state string) string {
	return "/auth/github/callback?state=" + state + "&code=mock-code"
}

// ListUserInstallations reports the one canned installation for any token, so
// the offline flow resolves an installation the way a returning production
// sign-in does.
func (c *Client) ListUserInstallations(context.Context, string) ([]githubapi.Installation, error) {
	return []githubapi.Installation{
		{ID: MockInstallationID, AccountLogin: "keyless-user"},
	}, nil
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
