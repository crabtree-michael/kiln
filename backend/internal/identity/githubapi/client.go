// Package githubapi is the GitHub HTTP adapter — the one place GitHub's own
// vocabulary is legal (11 §2). It has two halves, both belonging to ONE GitHub
// App:
//
//   - user authorization (this file): exchange the callback code for a USER
//     access token, fetch the authenticated user's profile, and list repos as
//     that user. This is the same three-legged web flow the OAuth App used —
//     a GitHub App's user-authorization half speaks the identical endpoint —
//     which is why converting the login flow (design 2026-08-04) needed no new
//     token exchange, only a different client id, a different redirect target,
//     and the installation that comes back with it.
//   - installation (app.go): build the install URL, sign an app JWT, mint the
//     short-lived installation tokens git and `gh` authenticate with, and list
//     one installation's repos.
//
// The user token answers "who is this and what can they see"; the installation
// token is the repo credential. Keeping them apart is the point: the first is
// stored, the second is minted per hour and never is.
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
// revoked, expired, or never had the reach the call needs. Callers treat it as
// "not connected" and send the user back through sign-in to re-authorize,
// rather than as a transport failure. Wraps ErrListRepos so either sentinel
// matches.
var ErrUnauthorized = fmt.Errorf("%w: token rejected", ErrListRepos)

// Config configures a Client. ClientID/ClientSecret are the App's own OAuth
// credentials, for the user-authorization half (the secret never leaves the
// backend). OAuthBaseURL and APIBaseURL default to GitHub's public hosts;
// overridable for tests.
//
// AppID/AppSlug/AppPrivateKey are the installation half (app.go, design
// 2026-08-04 §5). A client built without them still serves every user-token
// call; only the mint refuses, with ErrNoAppCredentials. That is a deliberate
// shape rather than an invitation to run half-configured: enforcing the full
// set is the composition root's boot gate, and this adapter answering an error
// instead of panicking is what keeps a misconfigured deploy serving 500s on one
// route rather than refusing to boot at all.
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

// Client is the GitHub adapter — the App's user-authorization flow (this file)
// and its installation flow (app.go).
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

// ConfigureURL is GitHub's own settings page for one installation — where the
// user changes WHICH repositories Kiln may touch (design §3.5). The dashboard
// links out to it rather than reimplementing a screen only GitHub can render,
// and only GitHub can honour.
//
// One URL shape covers both personal and organisation installations: GitHub
// redirects an org installation to its org-scoped page, so the caller does not
// have to know which kind it is holding.
func (c *Client) ConfigureURL(installationID int64) string {
	if installationID == 0 {
		return ""
	}
	return c.oauthBaseURL + "/settings/installations/" + strconv.FormatInt(installationID, 10)
}

// AuthorizeURL builds the redirect that SIGNS A USER IN: GitHub's ordinary
// user-authorization screen for this App (design §3.2, amended 2026-08-06).
//
// It exists because InstallURL cannot do this job twice. `installations/new`
// only completes for an account that has NOT installed the App: once it has,
// GitHub answers that URL with the installation's own configure page and never
// calls the callback, so a returning user — the same person on a second device,
// or anyone signing in again after their first visit — was left stranded on
// GitHub with no way back into Kiln. The authorize endpoint has no such state:
// it redirects to the callback with a `code` every time, silently for a user who
// has already authorized, so signing in stays one click for the returning case
// this replaced.
//
// What it does NOT yield is an installation. A first-time user authorizes here
// and arrives carrying nothing to clone with, which is why the caller resolves
// the installation separately (ListUserInstallations) and sends anyone still
// without one on to InstallURL.
//
// No `redirect_uri`: the App's registered callback URL is the one destination,
// and passing the parameter would only add a second place for it to be wrong.
func (c *Client) AuthorizeURL(state string) string {
	q := url.Values{"client_id": {c.clientID}, "state": {state}}
	return c.oauthBaseURL + "/login/oauth/authorize?" + q.Encode()
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
//
// `scope` is deliberately not decoded. A GitHub App's user token carries no
// scopes: what Kiln may do is the App's registered permissions, and what it may
// touch is the installation's repository selection. Reading a scope string here
// would be reading a field that is empty by construction and inviting a decision
// to be made on it.
type exchangeResponse struct {
	AccessToken      string `json:"access_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// ExchangeCode exchanges a callback code for the USER access token — the
// credential that answers `GET /user` (who signed in) and lists the caller's own
// view of an installation's repositories. It is NOT the repo credential; git and
// `gh` use a minted installation token (app.go).
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

// FetchUser fetches the authenticated user's profile using a user access token
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
// on, and org-member repos, private ones included. Results come back sorted by
// full name, the order the picker shows them in, and are capped at
// reposPerPage×maxRepoPages.
//
// Under the GitHub App this is the FALLBACK listing, for a credential that has
// no installation to narrow it: a hand-typed PAT, or the deployment's bootstrap
// token. The App path uses ListInstallationRepos, which shows only what the user
// picked — that narrowing is the whole point, and this function cannot do it.
//
// A token GitHub rejects yields ErrUnauthorized so the caller can prompt a
// re-authorize instead of reporting an outage.
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
// named error return lets the deferred body-close surface its error without a
// blank assignment (errcheck check-blank), the same shape as doExchange.
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
