// Package githubapi is the GitHub HTTP adapter — the one place GitHub's own
// vocabulary is legal (11 §2). It has two halves:
//
//   - OAuth App (this file): the three-legged web application flow — build the
//     authorize URL, exchange the callback code for an access token, fetch the
//     authenticated user's profile, and list the repos that token can reach.
//   - GitHub App (app.go): the installation flow the repo-selection migration
//     moves to — build the install URL, sign an app JWT, mint short-lived
//     installation tokens, and list one installation's repos.
//
// Both halves are live during the migration (design 2026-08-04 §6): an
// installation is used when one exists, and the stored OAuth token is the
// fallback until every user has moved.
//
// ClientSecret and the App private key never leave the backend (02 §2) — the
// secret is only ever sent, over HTTPS, as part of the token exchange request
// body, and the private key is only ever used to sign locally.
//
// This package is standalone: it knows nothing about Kiln's identity domain
// (users, sessions, allowlists). A later layer (internal/identity) composes
// it with that domain logic.
package githubapi

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Default base URLs for GitHub's web and API hosts, overridable in Config for
// tests (an httptest.Server) or GitHub Enterprise deployments.
const (
	DefaultOAuthBaseURL = "https://github.com"
	DefaultAPIBaseURL   = "https://api.github.com"

	requestTimeout = 10 * time.Second
	// maxErrorBody caps how much of a provider error response we read.
	maxErrorBody = 1 << 20

	// reposPerPage is GitHub's maximum page size for /user/repos, and
	// maxRepoPages bounds how many pages one listing walks — an account with
	// thousands of repos must not stall the request. Together: 500 repos.
	reposPerPage = 100
	maxRepoPages = 5
)

// ErrExchange is the static base for a code-exchange failure (err113:
// wrapped static errors, never dynamic ones).
var ErrExchange = errors.New("githubapi: exchange code")

// ErrFetchUser is the static base for a user-fetch failure.
var ErrFetchUser = errors.New("githubapi: fetch user")

// ErrListRepos is the static base for a repo-listing failure.
var ErrListRepos = errors.New("githubapi: list repos")

// ErrUnauthorized reports that GitHub rejected the access token itself — it was
// revoked, or it predates the repo scope (a token minted before Kiln asked for
// it can read /user but not /user/repos). Callers treat it as "not connected"
// and send the user back through sign-in to re-authorize, rather than as a
// transport failure. Wraps ErrListRepos so either sentinel matches.
var ErrUnauthorized = fmt.Errorf("%w: token rejected", ErrListRepos)

// Config configures a Client. ClientID/ClientSecret are the app's OAuth
// credentials (the secret never leaves the backend). OAuthBaseURL and
// APIBaseURL default to GitHub's public hosts; overridable for tests.
//
// AppID/AppSlug/AppPrivateKey are the GitHub App half (app.go, design
// 2026-08-04 §5). They are OPTIONAL on purpose: during the OAuth-App → GitHub-App
// migration a deployment may be configured for either, and a client built
// without them still serves every OAuth call — only the mint refuses, with
// ErrNoAppCredentials. Enforcing that a deployment configures one or the other
// is the composition root's boot gate, not this adapter's.
type Config struct {
	ClientID     string
	ClientSecret string
	OAuthBaseURL string
	APIBaseURL   string

	// AppID is the App's numeric id, the `iss` of every app JWT.
	AppID string
	// AppSlug is the App's public-link slug, which builds InstallURL.
	AppSlug string
	// AppPrivateKey signs app JWTs. Parse it once at boot with
	// ParsePrivateKey so a malformed key fails the boot gate, not a sign-in.
	AppPrivateKey *rsa.PrivateKey
}

// Client is the GitHub adapter — OAuth App flow (this file) and GitHub App
// installation flow (app.go).
type Client struct {
	clientID     string
	clientSecret string
	oauthBaseURL string
	apiBaseURL   string
	appID        string
	appSlug      string
	appKey       *rsa.PrivateKey
	http         *http.Client
}

// New builds a Client, applying defaults for OAuthBaseURL/APIBaseURL and
// trimming any trailing slash so path joins never produce "//". A nil hc
// gets a 10s-timeout default client.
func New(cfg Config, hc *http.Client) *Client {
	oauthBaseURL := strings.TrimSuffix(cfg.OAuthBaseURL, "/")
	if oauthBaseURL == "" {
		oauthBaseURL = DefaultOAuthBaseURL
	}
	apiBaseURL := strings.TrimSuffix(cfg.APIBaseURL, "/")
	if apiBaseURL == "" {
		apiBaseURL = DefaultAPIBaseURL
	}
	if hc == nil {
		hc = &http.Client{Timeout: requestTimeout}
	}
	return &Client{
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		oauthBaseURL: oauthBaseURL,
		apiBaseURL:   apiBaseURL,
		appID:        cfg.AppID,
		appSlug:      cfg.AppSlug,
		appKey:       cfg.AppPrivateKey,
		http:         hc,
	}
}

// ScopeRepo is GitHub's full read/write repository scope — what an access
// token needs to clone a private repo, push to it, and read/write its pull
// requests. It is what the dashboard's "Connect GitHub" grant requests; plain
// sign-in requests nothing (see AuthorizeURL).
const ScopeRepo = "repo"

// AuthorizeURL builds the GitHub authorize-redirect URL for the given OAuth
// state nonce and scope. An EMPTY scope sends no `scope` param at all, which
// is the plain sign-in grant: default public identity only (11 §2 D2 — signing
// in never asks for repo access). A non-empty scope (ScopeRepo) is the
// dashboard's "Connect GitHub" grant, whose token is stored as the user's repo
// credential and backs the settings repo picker.
//
// No redirect_uri is sent in either case: GitHub uses the OAuth app's single
// registered callback URL, which is why both grants share one callback route
// and neither needs a second registration.
func (c *Client) AuthorizeURL(state, scope string) string {
	q := url.Values{
		"client_id": {c.clientID},
		"state":     {state},
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	return fmt.Sprintf("%s/login/oauth/authorize?%s", c.oauthBaseURL, q.Encode())
}

// exchangeRequest is the POST /login/oauth/access_token body.
type exchangeRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Code         string `json:"code"`
}

// exchangeResponse is GitHub's token-exchange response body. GitHub returns
// HTTP 200 even for OAuth errors, signaled instead by a populated Error
// field — both that case and a non-2xx HTTP status must be handled. Scope is
// the comma-separated list GitHub actually granted, which can be NARROWER than
// what the authorize URL asked for (the user may decline an org, or the OAuth
// app may not be approved there) — so it is the granted scope, never the
// requested one, that decides whether the token is a usable repo credential.
type exchangeResponse struct {
	AccessToken      string `json:"access_token"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// ExchangeCode exchanges an OAuth callback code for an access token, returning
// the token and the scope string GitHub granted it (see exchangeResponse). A
// plain sign-in grant returns an empty scope; that is a real answer ("no
// scopes"), not an unknown one.
func (c *Client) ExchangeCode(ctx context.Context, code string) (string, string, error) {
	var body exchangeResponse
	if err := c.doExchange(ctx, code, &body); err != nil {
		return "", "", err
	}
	if body.Error != "" {
		return "", "", fmt.Errorf("githubapi: exchange: %w: %s (%s)", ErrExchange, body.Error, body.ErrorDescription)
	}
	if body.AccessToken == "" {
		return "", "", fmt.Errorf("githubapi: exchange: %w: empty access token in response", ErrExchange)
	}
	return body.AccessToken, body.Scope, nil
}

// GitHubUser is the subset of GitHub's `/user` response Kiln cares about.
// Fields are populated verbatim (e.g. Login case-preserved); lower-casing
// for the allowlist/storage key is the identity service's job, not this
// package's.
type GitHubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

// FetchUser fetches the authenticated user's profile using an access token
// obtained from ExchangeCode.
func (c *Client) FetchUser(ctx context.Context, accessToken string) (GitHubUser, error) {
	var user GitHubUser
	if _, err := c.doFetchUser(ctx, accessToken, &user); err != nil {
		return GitHubUser{}, err
	}
	return user, nil
}

// Repo is the subset of a GitHub repository Kiln's picker needs: the
// `owner/name` label it lists, the https URL it stores as the project's
// repo_url, and whether it is private (shown as a hint next to the label).
type Repo struct {
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
	Private  bool   `json:"private"`
}

// ListRepos returns the repos the access token can reach — owned, collaborated
// on, and org-member repos, private ones included (that is what the repo scope
// buys). Results come back sorted by full name, the order the picker shows them
// in, and are capped at reposPerPage×maxRepoPages.
//
// A token GitHub rejects (revoked, or minted before Kiln requested the repo
// scope) yields ErrUnauthorized so the caller can prompt a re-authorize instead
// of reporting an outage.
func (c *Client) ListRepos(ctx context.Context, accessToken string) ([]Repo, error) {
	repos := make([]Repo, 0, reposPerPage)
	for page := 1; page <= maxRepoPages; page++ {
		var batch []Repo
		if err := c.doListRepos(ctx, accessToken, page, &batch); err != nil {
			return nil, err
		}
		repos = append(repos, batch...)
		// A short page is the last page — GitHub fills every page but the final
		// one, so this avoids a wasted round trip on the exact-multiple case.
		if len(batch) < reposPerPage {
			break
		}
	}
	return repos, nil
}

// scopesHeader is the response header GitHub stamps on every authenticated API
// response, listing the scopes the presented token actually carries. Spelled in
// Go's canonical MIME form (http.Header canonicalizes on both set and get, so
// this matches GitHub's "X-OAuth-Scopes" on the wire).
const scopesHeader = "X-Oauth-Scopes"

// TokenScopes reports the scopes an EXISTING access token carries, by making
// the cheapest authenticated call there is (`GET /user`) and reading GitHub's
// X-OAuth-Scopes response header.
//
// This is how a credential Kiln did not mint — a personal access token typed
// into the old manual field, carried forward by the integrations redesign —
// gets classified without asking the user to re-enter anything. An error means
// "could not determine" (network, or the token is revoked/invalid); it never
// means "no scopes", so callers must not downgrade a stored credential on it.
//
// Fine-grained PATs report no scopes here (they carry resource permissions
// instead), so they read as unknown rather than insufficient — deliberately,
// since a working fine-grained token must not be declared broken.
func (c *Client) TokenScopes(ctx context.Context, accessToken string) (string, error) {
	var user GitHubUser
	header, err := c.doFetchUser(ctx, accessToken, &user)
	if err != nil {
		return "", err
	}
	return header.Get(scopesHeader), nil
}

// doListRepos issues GET /user/repos for one page and decodes the 200 body into
// out. The lone named error return lets the deferred body-close surface its
// error without a blank assignment (errcheck check-blank), the same shape as
// doExchange.
func (c *Client) doListRepos(ctx context.Context, accessToken string, page int, out *[]Repo) (err error) {
	q := url.Values{
		"per_page": {strconv.Itoa(reposPerPage)},
		"page":     {strconv.Itoa(page)},
		"sort":     {"full_name"},
		// Everything the user can actually push to, not just what they own —
		// an org repo they collaborate on is the common case for real work.
		"affiliation": {"owner,collaborator,organization_member"},
	}
	u := c.apiBaseURL + "/user/repos?" + q.Encode()
	req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if rerr != nil {
		return fmt.Errorf("githubapi: build list-repos request: %w", rerr)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, derr := c.http.Do(req)
	if derr != nil {
		return fmt.Errorf("githubapi: list repos: %w: %w", ErrListRepos, derr)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("githubapi: close list-repos response: %w", cerr)
		}
	}()

	// 401 = the token is dead; 403 = it is alive but not scoped for this read
	// (GitHub's answer for a token minted before Kiln asked for `repo`). Both
	// mean "re-authorize", not "GitHub is down".
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("githubapi: list repos: %w: http %d", ErrUnauthorized, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		errBody, berr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		if berr != nil {
			return fmt.Errorf("githubapi: list repos: %w: http %d", ErrListRepos, resp.StatusCode)
		}
		return fmt.Errorf("githubapi: list repos: %w: http %d: %s", ErrListRepos, resp.StatusCode, string(errBody))
	}

	if jerr := json.NewDecoder(resp.Body).Decode(out); jerr != nil {
		return fmt.Errorf("githubapi: decode repos response: %w", jerr)
	}
	return nil
}

// doExchange issues POST /login/oauth/access_token and decodes the response
// body into out, regardless of whether it signals an OAuth error. The lone
// named error return lets the deferred body-close surface its error without
// a blank assignment (errcheck check-blank), the same shape as the
// assemblyai adapter's fetchToken.
func (c *Client) doExchange(ctx context.Context, code string, out *exchangeResponse) (err error) {
	//nolint:gosec // G117: this *is* the OAuth token-exchange request body; the secret belongs here.
	reqBody, merr := json.Marshal(exchangeRequest{
		ClientID:     c.clientID,
		ClientSecret: c.clientSecret,
		Code:         code,
	})
	if merr != nil {
		return fmt.Errorf("githubapi: marshal exchange request: %w", merr)
	}

	u := c.oauthBaseURL + "/login/oauth/access_token"
	req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(reqBody))
	if rerr != nil {
		return fmt.Errorf("githubapi: build exchange request: %w", rerr)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, derr := c.http.Do(req)
	if derr != nil {
		return fmt.Errorf("githubapi: exchange: %w: %w", ErrExchange, derr)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("githubapi: close exchange response: %w", cerr)
		}
	}()

	if resp.StatusCode >= http.StatusBadRequest {
		errBody, rerr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		if rerr != nil {
			return fmt.Errorf("githubapi: exchange: %w: http %d", ErrExchange, resp.StatusCode)
		}
		return fmt.Errorf("githubapi: exchange: %w: http %d: %s", ErrExchange, resp.StatusCode, string(errBody))
	}

	if jerr := json.NewDecoder(resp.Body).Decode(out); jerr != nil {
		return fmt.Errorf("githubapi: decode exchange response: %w", jerr)
	}
	return nil
}

// doFetchUser issues GET /user, decodes the 200 body into out, and returns the
// response headers (TokenScopes reads X-OAuth-Scopes off them; FetchUser
// ignores them). The lone named error return lets the deferred body-close
// surface its error without a blank assignment (errcheck check-blank), the
// same shape as doExchange.
func (c *Client) doFetchUser(
	ctx context.Context, accessToken string, out *GitHubUser,
) (_ http.Header, err error) {
	u := c.apiBaseURL + "/user"
	req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if rerr != nil {
		return nil, fmt.Errorf("githubapi: build fetch-user request: %w", rerr)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, derr := c.http.Do(req)
	if derr != nil {
		return nil, fmt.Errorf("githubapi: fetch user: %w: %w", ErrFetchUser, derr)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("githubapi: close fetch-user response: %w", cerr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		errBody, rerr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		if rerr != nil {
			return nil, fmt.Errorf("githubapi: fetch user: %w: http %d", ErrFetchUser, resp.StatusCode)
		}
		return nil, fmt.Errorf("githubapi: fetch user: %w: http %d: %s", ErrFetchUser, resp.StatusCode, string(errBody))
	}

	if jerr := json.NewDecoder(resp.Body).Decode(out); jerr != nil {
		return nil, fmt.Errorf("githubapi: decode user response: %w", jerr)
	}
	return resp.Header, nil
}
