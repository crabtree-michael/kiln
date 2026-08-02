// Package githubapi is the GitHub OAuth HTTP adapter — the one place
// GitHub-OAuth vocabulary (authorize URL, code exchange, `/user`, `/user/repos`)
// is legal (11 §2). It performs the three-legged OAuth web application flow:
// build the authorize URL, exchange the callback code for an access token, fetch
// the authenticated user's profile, and list the repos that token can reach.
// ClientSecret never leaves the backend (02 §2) — it is only ever sent, over
// HTTPS, as part of the token exchange request body.
//
// This package is standalone: it knows nothing about Kiln's identity domain
// (users, sessions, allowlists). A later layer (internal/identity) composes
// it with that domain logic.
package githubapi

import (
	"bytes"
	"context"
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

	// repoScope is the one OAuth scope Kiln asks for beyond the default public
	// identity. Sign-in and repo access are the SAME credential (there is no
	// second OAuth app or callback): the token the callback exchanges is what
	// lists the repos in the settings picker and what clones the one the user
	// selects, private repos included. Widening this scope means every user
	// re-consents on their next sign-in.
	repoScope = "repo"

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

// Config configures a Client. ClientID/ClientSecret are the OAuth app's
// credentials (GITHUB_OAUTH_CLIENT_ID / GITHUB_OAUTH_CLIENT_SECRET, the
// latter a secret that never leaves the backend). OAuthBaseURL and
// APIBaseURL default to GitHub's public hosts; overridable for tests.
type Config struct {
	ClientID     string
	ClientSecret string
	OAuthBaseURL string
	APIBaseURL   string
}

// Client is the GitHub OAuth adapter.
type Client struct {
	clientID     string
	clientSecret string
	oauthBaseURL string
	apiBaseURL   string
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
		http:         hc,
	}
}

// AuthorizeURL builds the GitHub authorize-redirect URL for the given OAuth
// state nonce. It requests the `repo` scope on top of the default public
// identity, because sign-in and repo access are one credential: the resulting
// token both identifies the user and backs the settings repo picker. No
// redirect_uri is sent — GitHub uses the OAuth app's single registered callback
// URL (11 §2), which is exactly why there is no second flow to register.
func (c *Client) AuthorizeURL(state string) string {
	q := url.Values{
		"client_id": {c.clientID},
		"state":     {state},
		"scope":     {repoScope},
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
// field — both that case and a non-2xx HTTP status must be handled.
type exchangeResponse struct {
	AccessToken      string `json:"access_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// ExchangeCode exchanges an OAuth callback code for an access token.
func (c *Client) ExchangeCode(ctx context.Context, code string) (string, error) {
	var body exchangeResponse
	if err := c.doExchange(ctx, code, &body); err != nil {
		return "", err
	}
	if body.Error != "" {
		return "", fmt.Errorf("githubapi: exchange: %w: %s (%s)", ErrExchange, body.Error, body.ErrorDescription)
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("githubapi: exchange: %w: empty access token in response", ErrExchange)
	}
	return body.AccessToken, nil
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
	if err := c.doFetchUser(ctx, accessToken, &user); err != nil {
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

// doFetchUser issues GET /user and decodes the 200 body into out. The lone
// named error return lets the deferred body-close surface its error without
// a blank assignment (errcheck check-blank), the same shape as doExchange.
func (c *Client) doFetchUser(ctx context.Context, accessToken string, out *GitHubUser) (err error) {
	u := c.apiBaseURL + "/user"
	req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if rerr != nil {
		return fmt.Errorf("githubapi: build fetch-user request: %w", rerr)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, derr := c.http.Do(req)
	if derr != nil {
		return fmt.Errorf("githubapi: fetch user: %w: %w", ErrFetchUser, derr)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("githubapi: close fetch-user response: %w", cerr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		errBody, rerr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		if rerr != nil {
			return fmt.Errorf("githubapi: fetch user: %w: http %d", ErrFetchUser, resp.StatusCode)
		}
		return fmt.Errorf("githubapi: fetch user: %w: http %d: %s", ErrFetchUser, resp.StatusCode, string(errBody))
	}

	if jerr := json.NewDecoder(resp.Body).Decode(out); jerr != nil {
		return fmt.Errorf("githubapi: decode user response: %w", jerr)
	}
	return nil
}
