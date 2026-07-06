// Package githubapi is the GitHub OAuth HTTP adapter — the one place
// GitHub-OAuth vocabulary (authorize URL, code exchange, `/user`) is legal
// (11 §2). It performs the three-legged OAuth web application flow: build the
// authorize URL, exchange the callback code for an access token, and fetch
// the authenticated user's profile. ClientSecret never leaves the backend
// (02 §2) — it is only ever sent, over HTTPS, as part of the token exchange
// request body.
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
)

// ErrExchange is the static base for a code-exchange failure (err113:
// wrapped static errors, never dynamic ones).
var ErrExchange = errors.New("githubapi: exchange code")

// ErrFetchUser is the static base for a user-fetch failure.
var ErrFetchUser = errors.New("githubapi: fetch user")

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
// state nonce. No scopes and no redirect_uri are sent: Kiln requests only
// default public identity, and GitHub uses the OAuth app's registered
// callback URL (11 §2).
func (c *Client) AuthorizeURL(state string) string {
	q := url.Values{
		"client_id": {c.clientID},
		"state":     {state},
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
