// Package githubmock is the offline stand-in for the GitHub OAuth/API adapter,
// selected by KILN_GITHUB_MODE=mock (keyless e2e design §3, mirroring
// AGENT_MODE=mock / KILN_VOICE_MODE=mock / KILN_VERIFY_MODE=mock).
//
// It follows the same principle as the mock agent provider: fake ONLY the
// vendor. Sessions, the credential store, the repo-picker endpoint, and the
// dashboard form all run for real — only the calls that would reach github.com
// are canned. That is what lets the keyless lane exercise the real onboarding
// form, whose repo picker can no longer be typed into.
package githubmock

import (
	"context"

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
