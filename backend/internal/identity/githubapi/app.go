// The GitHub App's installation half, beside its user-authorization half
// (client.go).
//
// The difference that matters, and the reason the OAuth App this replaced is
// gone: an OAuth App's token was long-lived, account-wide, and stored; an
// installation token is minted on demand from the App's private key, scoped to
// ONE installation — the repositories the user picked on GitHub's own chooser —
// and dies within the hour (design 2026-08-04 §3.3). The only long-lived secret
// left is the private key, which never leaves the backend.
//
// The mint is two hops, both here:
//
//	App private key --RS256--> app JWT --POST--> installation access token
//
// The JWT authenticates Kiln AS THE APP (it proves possession of the private
// key and carries no user); the installation token is what git and `gh` actually
// authenticate with. Nothing in this file knows about users, sessions, or the
// allowlist — composing it with that is internal/identity's job, as with the
// rest of the package.
//
// (Detached from the package clause on purpose: the package comment lives at the
// top of client.go, and Go would treat a second one here as a duplicate.)

package githubapi

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// jwtLifetime is how long an app JWT stays valid. GitHub REJECTS anything
	// over 10 minutes, so this sits a minute under the ceiling rather than at
	// it — an exp computed right on the boundary is the kind of thing that
	// works locally and fails against a server whose clock runs slow.
	jwtLifetime = 9 * time.Minute
	// jwtClockSkew backdates iat. GitHub rejects a JWT issued in ITS future,
	// which is what a host clock a few seconds fast produces; a minute of slack
	// costs nothing and removes a whole class of intermittent 401.
	jwtClockSkew = 60 * time.Second
)

// ErrMintToken is the static base for an installation-token mint failure
// (err113: wrapped static errors, never dynamic ones).
var ErrMintToken = errors.New("githubapi: mint installation token")

// ErrInstallationUnavailable reports that the installation itself is gone —
// uninstalled, suspended, or never existed — rather than that the mint call
// failed in transit. It is the App-era counterpart of ErrUnauthorized: callers
// treat it as "not connected" and send the user back through the install flow,
// never as an outage. Wraps ErrMintToken so either sentinel matches.
var ErrInstallationUnavailable = fmt.Errorf("%w: installation unavailable", ErrMintToken)

// ErrNoAppCredentials reports that this client was built without an App ID and
// private key, so it cannot mint anything. The composition root's boot gate is
// what should prevent this (design §5); returning an error rather than panicking
// keeps a misconfigured deployment answering 500 on one route instead of
// refusing to serve at all.
var ErrNoAppCredentials = fmt.Errorf("%w: no app id/private key configured", ErrMintToken)

// ErrParsePrivateKey is the static base for a malformed App private key.
var ErrParsePrivateKey = errors.New("githubapi: parse app private key")

// RepositorySelectionAll is the value GitHub reports for an installation the
// user granted every repository; RepositorySelectionSelected is the narrowed
// choice this migration exists to offer. Both are GitHub's spellings.
const (
	RepositorySelectionAll      = "all"
	RepositorySelectionSelected = "selected"
)

// ParsePrivateKey parses the PEM GitHub generates when you create an App's
// private key. GitHub emits PKCS#1 ("BEGIN RSA PRIVATE KEY"); PKCS#8 ("BEGIN
// PRIVATE KEY") is accepted too, because the difference is invisible in the
// dashboard and a key round-tripped through openssl or a secret store often
// comes back in the other form. Anything else — an encrypted key, an EC key, a
// truncated paste — is a boot-time error, not a runtime surprise.
//
// The composition root calls this once at startup, so a bad key fails the boot
// gate rather than the first user's sign-in.
func ParsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("%w: no PEM block found", ErrParsePrivateKey)
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrParsePrivateKey, err)
	}
	return key, nil
}

// InstallURL builds the redirect that starts THE GitHub grant (design §3.2).
// It is the target of `/auth/github/connect`, replacing the OAuth authorize URL
// that used to sit there: this URL is where GitHub renders the "All
// repositories / Only select repositories" chooser.
//
// With "Request user authorization (OAuth) during installation" enabled on the
// App, one trip through this URL yields BOTH the installation (the repository
// choice) and a user-authorization `code` for the existing identity path — which
// is why the migration needs no second route and no second callback.
//
// The state nonce round-trips exactly as it did under the OAuth authorize URL,
// so the callback's CSRF check was unchanged by the move.
func (c *Client) InstallURL(state string) string {
	q := url.Values{"state": {state}}
	return fmt.Sprintf("%s/apps/%s/installations/new?%s", c.oauthBaseURL, c.appSlug, q.Encode())
}

// InstallationToken is a minted installation access token: the credential git,
// `gh`, and the repo listing authenticate with. ExpiresAt is GitHub's own
// expiry (one hour out at time of writing) rather than one this package
// computes — callers cache against what the server said, never against an
// assumption about GitHub's policy.
//
// RepositorySelection is "all" or "selected" (see the constants): the
// user's answer to the chooser, which the dashboard renders so somebody who
// picked three repos can see that Kiln knows it.
type InstallationToken struct {
	Token               string
	ExpiresAt           time.Time
	RepositorySelection string
}

// appJWT builds and signs the short-lived JWT that authenticates Kiln as the
// App itself. `iss` is the App ID; there is no subject and no audience, because
// the App is the only principal involved — the installation is named in the
// mint URL's path, not in the token.
//
// This is the ONLY use of the private key. It never leaves this method, and the
// JWT it produces is never stored: it lives for one mint call.
func (c *Client) appJWT(now time.Time) (string, error) {
	if c.appID == "" || c.appKey == nil {
		return "", ErrNoAppCredentials
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Issuer:    c.appID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-jwtClockSkew)),
		ExpiresAt: jwt.NewNumericDate(now.Add(jwtLifetime)),
	})
	signed, err := tok.SignedString(c.appKey)
	if err != nil {
		return "", fmt.Errorf("githubapi: sign app jwt: %w: %w", ErrMintToken, err)
	}
	return signed, nil
}

// mintResponse is GitHub's POST .../access_tokens response body. Only the three
// fields Kiln acts on are decoded; `permissions` is deliberately ignored, since
// what the App may do is decided at registration (design §4) and re-deriving it
// per mint would just be a second, staler copy of that decision.
type mintResponse struct {
	Token               string    `json:"token"`
	ExpiresAt           time.Time `json:"expires_at"`
	RepositorySelection string    `json:"repository_selection"`
}

// MintInstallationToken exchanges the App JWT for an installation access token
// scoped to installationID — the credential every authenticated repo operation
// then uses (design §3.3).
//
// It is a NETWORK CALL on the hot path of git/`gh` invocations, so the caller is
// expected to cache the result until shortly before ExpiresAt rather than mint
// per operation. That cache is identity's, not this package's: this adapter
// stays a thin, stateless translation of GitHub's HTTP surface, the same way
// ExchangeCode does not remember tokens either.
//
// A gone/suspended installation yields ErrInstallationUnavailable so the caller
// can render "reconnect" instead of "GitHub is down".
func (c *Client) MintInstallationToken(ctx context.Context, installationID int64) (InstallationToken, error) {
	jwtStr, err := c.appJWT(time.Now())
	if err != nil {
		return InstallationToken{}, err
	}
	var body mintResponse
	if derr := c.doMint(ctx, jwtStr, installationID, &body); derr != nil {
		return InstallationToken{}, derr
	}
	if body.Token == "" {
		return InstallationToken{}, fmt.Errorf("githubapi: mint: %w: empty token in response", ErrMintToken)
	}
	// A field-for-field conversion: mintResponse is InstallationToken plus the
	// json tags, and keeping them as two named types is what stops GitHub's
	// wire shape from leaking into the package's public vocabulary.
	return InstallationToken(body), nil
}

// doMint issues POST /app/installations/{id}/access_tokens and decodes the
// created body into out. The lone named error return lets the deferred
// body-close surface its error without a blank assignment (errcheck
// check-blank), the same shape as doExchange.
func (c *Client) doMint(ctx context.Context, appJWT string, installationID int64, out *mintResponse) (err error) {
	u := c.apiBaseURL + "/app/installations/" + strconv.FormatInt(installationID, 10) + "/access_tokens"
	req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if rerr != nil {
		return fmt.Errorf("githubapi: build mint request: %w", rerr)
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, derr := c.http.Do(req)
	if derr != nil {
		return fmt.Errorf("githubapi: mint: %w: %w", ErrMintToken, derr)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("githubapi: close mint response: %w", cerr)
		}
	}()

	// 401 = the JWT was rejected (bad key, or a clock far enough off that even
	// jwtClockSkew did not save it); 403/404 = the App is not installed there
	// any more, or the installation is suspended. All three mean "the user must
	// go through the install flow again", not "retry later".
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return fmt.Errorf("githubapi: mint: %w: http %d", ErrInstallationUnavailable, resp.StatusCode)
	// GitHub answers 201 Created here, not 200 — accept both rather than pin
	// the exact code, so a future 200 does not read as a failure.
	case http.StatusCreated, http.StatusOK:
	default:
		errBody, berr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		if berr != nil {
			return fmt.Errorf("githubapi: mint: %w: http %d", ErrMintToken, resp.StatusCode)
		}
		return fmt.Errorf("githubapi: mint: %w: http %d: %s", ErrMintToken, resp.StatusCode, string(errBody))
	}

	if jerr := json.NewDecoder(resp.Body).Decode(out); jerr != nil {
		return fmt.Errorf("githubapi: decode mint response: %w", jerr)
	}
	return nil
}

// installationReposStatus maps one installation-listing response status onto an
// error, or nil when the page is readable. Split out of the request function so
// the status vocabulary lives in one place rather than inline among the
// transport plumbing.
//
// The split matches doListRepos — 401 the token is dead, 403 it cannot read
// this installation — and adds 404, which here is meaningful rather than
// impossible: it is GitHub's answer when the installation is gone for this user,
// which is "re-authorize", not "GitHub is down".
func installationReposStatus(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return fmt.Errorf("githubapi: list installation repos: %w: http %d", ErrUnauthorized, resp.StatusCode)
	case http.StatusOK:
		return nil
	}
	errBody, berr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if berr != nil {
		return fmt.Errorf("githubapi: list installation repos: %w: http %d", ErrListRepos, resp.StatusCode)
	}
	return fmt.Errorf(
		"githubapi: list installation repos: %w: http %d: %s", ErrListRepos, resp.StatusCode, string(errBody),
	)
}

// installationReposResponse is GitHub's envelope for the installation-scoped
// repo listings — unlike /user/repos, which is a bare array, these wrap the page
// in an object. Only the repositories are decoded; total_count is redundant with
// walking the pages.
type installationReposResponse struct {
	Repositories []Repo `json:"repositories"`
}

// ListInstallationRepos returns the repos of ONE installation that the calling
// user can themselves see — the App-era replacement for ListRepos as the source
// of the settings/onboarding repo picker (design §3.3).
//
// This is where the migration's user-visible payoff lands: the listing is
// exactly the set the user ticked on GitHub's chooser, so the picker stops
// offering every repository the account can reach.
//
// It is deliberately the USER-scoped endpoint (/user/installations/{id}/...)
// rather than the installation-wide one. In an org whose installation covers
// repositories a given member cannot access, the installation-wide listing would
// offer them repos they have no business pointing a project at; this endpoint
// intersects the installation with the caller's own access, which is the honest
// set. The cost is that accessToken must be a USER access token, not the minted
// installation token.
//
// Paging, ordering, and the cap match ListRepos, so the picker's behaviour is
// unchanged apart from the set it draws from. A rejected token yields
// ErrUnauthorized, exactly as ListRepos does.
func (c *Client) ListInstallationRepos(
	ctx context.Context, accessToken string, installationID int64,
) ([]Repo, error) {
	repos := make([]Repo, 0, reposPerPage)
	for page := 1; page <= maxRepoPages; page++ {
		var batch installationReposResponse
		if err := c.doListInstallationRepos(ctx, accessToken, installationID, page, &batch); err != nil {
			return nil, err
		}
		repos = append(repos, batch.Repositories...)
		// A short page is the last page — same reasoning as ListRepos.
		if len(batch.Repositories) < reposPerPage {
			break
		}
	}
	return repos, nil
}

// doListInstallationRepos issues GET /user/installations/{id}/repositories for
// one page and decodes the 200 body into out. The lone named error return lets
// the deferred body-close surface its error without a blank assignment
// (errcheck check-blank), the same shape as doListRepos.
func (c *Client) doListInstallationRepos(
	ctx context.Context, accessToken string, installationID int64, page int, out *installationReposResponse,
) (err error) {
	q := url.Values{
		"per_page": {strconv.Itoa(reposPerPage)},
		"page":     {strconv.Itoa(page)},
	}
	// No `sort` param: this endpoint does not accept one (unlike /user/repos),
	// so ordering is normalized by the caller that builds the picker rather
	// than asked for here.
	u := c.apiBaseURL + "/user/installations/" + strconv.FormatInt(installationID, 10) +
		"/repositories?" + q.Encode()
	req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if rerr != nil {
		return fmt.Errorf("githubapi: build list-installation-repos request: %w", rerr)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, derr := c.http.Do(req)
	if derr != nil {
		return fmt.Errorf("githubapi: list installation repos: %w: %w", ErrListRepos, derr)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("githubapi: close list-installation-repos response: %w", cerr)
		}
	}()

	if serr := installationReposStatus(resp); serr != nil {
		return serr
	}

	if jerr := json.NewDecoder(resp.Body).Decode(out); jerr != nil {
		return fmt.Errorf("githubapi: decode installation repos response: %w", jerr)
	}
	return nil
}
