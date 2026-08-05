package githubapi_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity/githubapi"
)

const (
	testAppID   = "app-4242"
	testAppSlug = "kiln-dev"
	// testInstallationID is the installation every test mints against — more
	// than one digit, so a mangled path join shows up in the assertion.
	testInstallationID = int64(987654)
	// testInstallToken is GitHub's `ghs_` installation-token prefix; testUserToken
	// is a user access token. The two are deliberately distinguishable, because
	// which one a call carries is a correctness property here, not a detail.
	testInstallToken = "ghs_installation"
	testUserToken    = "user-token"
	testExpiresAtRaw = "2026-08-05T12:00:00Z"
)

// mintBody is GitHub's POST .../access_tokens response, as a test builds it.
// Typed rather than a map so the field names appear once.
type mintBody struct {
	Token               string            `json:"token,omitempty"`
	ExpiresAt           string            `json:"expires_at,omitempty"`
	RepositorySelection string            `json:"repository_selection,omitempty"`
	Permissions         map[string]string `json:"permissions,omitempty"`
}

// reposPage is GitHub's envelope for an installation repo listing — the shape
// that differs from /user/repos' bare array.
type reposPage struct {
	TotalCount   int              `json:"total_count"`
	Repositories []githubapi.Repo `json:"repositories"`
}

// testKey generates a throwaway RSA key. 2048 bits is GitHub's own minimum and
// is fast enough to do per-test rather than sharing mutable state across them.
func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	return key
}

// pkcs1PEM re-encodes a key the way GitHub hands one out, so ParsePrivateKey is
// exercised against the real-world format rather than a convenient one.
func pkcs1PEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func pkcs8PEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// appClient builds a client wired to ts with App credentials present.
func appClient(t *testing.T, ts *httptest.Server, key *rsa.PrivateKey) *githubapi.Client {
	t.Helper()
	return githubapi.New(githubapi.Config{
		ClientID:      testClientID,
		ClientSecret:  testClientSecret,
		OAuthBaseURL:  ts.URL,
		APIBaseURL:    ts.URL,
		AppID:         testAppID,
		AppSlug:       testAppSlug,
		AppPrivateKey: key,
	}, ts.Client())
}

// writeJSON encodes v as the response body, failing the test rather than the
// handler if it cannot (errcheck: the write is not ignorable).
func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

// GitHub emits PKCS#1; a key round-tripped through a secret store often comes
// back as PKCS#8. Both must load, because the difference is invisible to whoever
// pastes the key into Render.
func TestParsePrivateKeyAcceptsBothPEMForms(t *testing.T) {
	key := testKey(t)

	for _, tc := range []struct {
		name string
		pem  []byte
	}{
		{"pkcs1", pkcs1PEM(t, key)},
		{"pkcs8", pkcs8PEM(t, key)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := githubapi.ParsePrivateKey(tc.pem)
			if err != nil {
				t.Fatalf("ParsePrivateKey(%s) = %v, want nil", tc.name, err)
			}
			if !got.Equal(key) {
				t.Errorf("ParsePrivateKey(%s) returned a different key", tc.name)
			}
		})
	}
}

// A malformed key must fail at parse time — that is what makes it a boot-gate
// failure (design §7) rather than a 500 on the first user's sign-in.
func TestParsePrivateKeyRejectsGarbage(t *testing.T) {
	for _, tc := range []struct {
		name string
		pem  string
	}{
		{"empty", ""},
		{"not pem", "just some text that is not a key"},
		{"pem block, junk payload", "-----BEGIN RSA PRIVATE KEY-----\nZm9vYmFy\n-----END RSA PRIVATE KEY-----\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := githubapi.ParsePrivateKey([]byte(tc.pem)); !errors.Is(err, githubapi.ErrParsePrivateKey) {
				t.Errorf("ParsePrivateKey(%s) error = %v, want ErrParsePrivateKey", tc.name, err)
			}
		})
	}
}

// The install URL replaces the authorize URL as `/auth/github/connect`'s target —
// it is the page that renders GitHub's repository chooser, so its shape is the
// whole feature.
func TestInstallURL(t *testing.T) {
	c := githubapi.New(githubapi.Config{
		OAuthBaseURL: testGitHubHost,
		AppSlug:      testAppSlug,
	}, nil)

	got := c.InstallURL(testState)

	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse InstallURL result: %v", err)
	}
	if want := "/apps/" + testAppSlug + "/installations/new"; u.Path != want {
		t.Errorf("path = %q, want %q", u.Path, want)
	}
	// The state nonce round-trips exactly as it does for the OAuth grant, so the
	// callback's CSRF check is unchanged by the migration.
	if u.Query().Get("state") != testState {
		t.Errorf("state = %q, want %q", u.Query().Get("state"), testState)
	}
	if !strings.HasPrefix(got, testGitHubHost+"/") {
		t.Errorf("InstallURL = %q, want prefix %s/", got, testGitHubHost)
	}
}

// The JWT is the App's proof of identity, so every claim GitHub validates is
// asserted here: the issuer it matches against the App, and a window it rejects
// if it is over 10 minutes or issued in GitHub's future.
func TestMintSignsAppJWT(t *testing.T) {
	var gotAuth, gotAccept, gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAccept = r.Header.Get("Authorization"), r.Header.Get("Accept")
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusCreated)
		writeJSON(t, w, mintBody{Token: testInstallToken, ExpiresAt: testExpiresAtRaw})
	}))
	defer ts.Close()

	before := time.Now()
	if _, err := appClient(t, ts, testKey(t)).MintInstallationToken(t.Context(), testInstallationID); err != nil {
		t.Fatalf("MintInstallationToken: %v", err)
	}
	after := time.Now()

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if want := "/app/installations/987654/access_tokens"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("Accept = %q, want application/vnd.github+json", gotAccept)
	}

	raw, ok := strings.CutPrefix(gotAuth, "Bearer ")
	if !ok {
		t.Fatalf("Authorization = %q, want a Bearer token", gotAuth)
	}
	claims := decodeJWTClaims(t, raw)

	if claims.Issuer != testAppID {
		t.Errorf("iss = %q, want %q", claims.Issuer, testAppID)
	}
	// iat is backdated a minute against clock skew, so it must sit before the
	// call started; exp must stay inside GitHub's 10-minute ceiling.
	iat, exp := time.Unix(claims.IssuedAt, 0), time.Unix(claims.ExpiresAt, 0)
	if !iat.Before(before) {
		t.Errorf("iat = %v, want backdated before the call at %v", iat, before)
	}
	if lifetime := exp.Sub(iat); lifetime > 10*time.Minute {
		t.Errorf("exp-iat = %v, want <= 10m (GitHub rejects longer)", lifetime)
	}
	if !exp.After(after) {
		t.Errorf("exp = %v, want after the call finished at %v", exp, after)
	}
}

// Guard against the signature being cosmetic: the JWT must verify against the
// App's PUBLIC key, which is what GitHub does with it.
func TestAppJWTVerifiesAgainstPublicKey(t *testing.T) {
	key := testKey(t)
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
		writeJSON(t, w, mintBody{Token: testInstallToken})
	}))
	defer ts.Close()

	if _, err := appClient(t, ts, key).MintInstallationToken(t.Context(), testInstallationID); err != nil {
		t.Fatalf("MintInstallationToken: %v", err)
	}

	raw := strings.TrimPrefix(gotAuth, "Bearer ")
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if verr := verifyRS256(&key.PublicKey, parts[0]+"."+parts[1], sig); verr != nil {
		t.Errorf("app JWT does not verify against the app public key: %v", verr)
	}
}

// The mint's three fields are what the caller acts on: the credential itself,
// the expiry it caches against, and the "all vs selected" answer the dashboard
// shows back to the user.
func TestMintInstallationTokenSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		writeJSON(t, w, mintBody{
			Token:               testInstallToken,
			ExpiresAt:           testExpiresAtRaw,
			RepositorySelection: githubapi.RepositorySelectionSelected,
			// Present in GitHub's real response and deliberately ignored.
			Permissions: map[string]string{"contents": "write"},
		})
	}))
	defer ts.Close()

	got, err := appClient(t, ts, testKey(t)).MintInstallationToken(t.Context(), testInstallationID)
	if err != nil {
		t.Fatalf("MintInstallationToken: %v", err)
	}

	if got.Token != testInstallToken {
		t.Errorf("Token = %q, want %q", got.Token, testInstallToken)
	}
	// The expiry is GitHub's own, parsed rather than assumed — callers cache
	// against what the server said, not against a guess at GitHub's policy.
	want := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	if !got.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, want)
	}
	if got.RepositorySelection != githubapi.RepositorySelectionSelected {
		t.Errorf("RepositorySelection = %q, want selected", got.RepositorySelection)
	}
}

// A 200 instead of GitHub's documented 201 must still read as success — pinning
// the exact code would turn a harmless server change into an outage.
func TestMintInstallationTokenAcceptsOK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, mintBody{Token: testInstallToken, RepositorySelection: githubapi.RepositorySelectionAll})
	}))
	defer ts.Close()

	got, err := appClient(t, ts, testKey(t)).MintInstallationToken(t.Context(), testInstallationID)
	if err != nil {
		t.Fatalf("MintInstallationToken: %v", err)
	}
	if got.Token != testInstallToken {
		t.Errorf("Token = %q, want %q", got.Token, testInstallToken)
	}
	if got.RepositorySelection != githubapi.RepositorySelectionAll {
		t.Errorf("RepositorySelection = %q, want all", got.RepositorySelection)
	}
}

// An uninstalled, suspended, or unknown installation is "the user must reinstall",
// not "GitHub is down" — the distinction the dashboard's Connected/Reconnect
// state turns on (design §3.4), so each status is pinned.
func TestMintInstallationTokenUnavailable(t *testing.T) {
	for _, status := range []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
	} {
		t.Run(fmt.Sprintf("http %d", status), func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			defer ts.Close()

			_, err := appClient(t, ts, testKey(t)).MintInstallationToken(t.Context(), testInstallationID)
			if !errors.Is(err, githubapi.ErrInstallationUnavailable) {
				t.Errorf("error = %v, want ErrInstallationUnavailable", err)
			}
			// It must also match the broader sentinel, so a caller that only
			// cares "the mint failed" keeps working.
			if !errors.Is(err, githubapi.ErrMintToken) {
				t.Errorf("error = %v, want it to wrap ErrMintToken", err)
			}
		})
	}
}

// A 5xx is an outage, NOT a reinstall prompt — the inverse of the case above,
// and the one that would be user-hostile to get wrong.
func TestMintInstallationTokenServerErrorIsNotUnavailable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := appClient(t, ts, testKey(t)).MintInstallationToken(t.Context(), testInstallationID)
	if !errors.Is(err, githubapi.ErrMintToken) {
		t.Fatalf("error = %v, want ErrMintToken", err)
	}
	if errors.Is(err, githubapi.ErrInstallationUnavailable) {
		t.Errorf("error = %v, must NOT be ErrInstallationUnavailable on a 5xx", err)
	}
}

// A 201 with no token is a broken response, not a usable empty credential —
// otherwise the empty string would travel on as a "token" and fail much later,
// inside git.
func TestMintInstallationTokenEmptyToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		writeJSON(t, w, mintBody{ExpiresAt: testExpiresAtRaw})
	}))
	defer ts.Close()

	if _, err := appClient(t, ts, testKey(t)).MintInstallationToken(
		t.Context(), testInstallationID,
	); !errors.Is(err, githubapi.ErrMintToken) {
		t.Errorf("error = %v, want ErrMintToken", err)
	}
}

// A client built for the OAuth half alone must not pretend it can mint. This is
// the migration's shape (§6): both configurations are legal, and the adapter
// says which one it is rather than panicking or signing with a nil key.
func TestMintWithoutAppCredentials(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer ts.Close()

	c := githubapi.New(githubapi.Config{
		ClientID: testClientID, ClientSecret: testClientSecret, APIBaseURL: ts.URL,
	}, ts.Client())

	_, err := c.MintInstallationToken(t.Context(), testInstallationID)
	if !errors.Is(err, githubapi.ErrNoAppCredentials) {
		t.Errorf("error = %v, want ErrNoAppCredentials", err)
	}
	if called {
		t.Error("expected no HTTP call without app credentials")
	}
}

// A cancelled context must abort the mint rather than hang or ignore it.
func TestMintHonoursContext(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		writeJSON(t, w, mintBody{Token: testInstallToken})
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := appClient(t, ts, testKey(t)).MintInstallationToken(ctx, testInstallationID); err == nil {
		t.Error("expected an error from a cancelled context")
	}
}

// The listing is the migration's user-visible payoff: the picker draws from the
// installation the user narrowed, via the user-scoped endpoint (design §3.3).
func TestListInstallationRepos(t *testing.T) {
	var gotPath, gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		writeJSON(t, w, reposPage{
			TotalCount: 2,
			Repositories: []githubapi.Repo{
				{FullName: testRepoName, HTMLURL: testRepoURL, Private: true},
				{FullName: "acme/web", HTMLURL: "https://github.com/acme/web", Private: false},
			},
		})
	}))
	defer ts.Close()

	got, err := appClient(t, ts, testKey(t)).ListInstallationRepos(t.Context(), testUserToken, testInstallationID)
	if err != nil {
		t.Fatalf("ListInstallationRepos: %v", err)
	}

	if want := "/user/installations/987654/repositories"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	// The USER token, not the minted installation token: in an org whose
	// installation covers repos a given member cannot reach, the installation-wide
	// listing would offer them repos they have no business pointing a project at.
	if gotAuth != "Bearer "+testUserToken {
		t.Errorf("Authorization = %q, want Bearer %s", gotAuth, testUserToken)
	}
	if len(got) != 2 {
		t.Fatalf("len(repos) = %d, want 2", len(got))
	}
	if got[0].FullName != testRepoName || !got[0].Private {
		t.Errorf("repos[0] = %+v, want %s private", got[0], testRepoName)
	}
	if got[1].FullName != "acme/web" || got[1].Private {
		t.Errorf("repos[1] = %+v, want acme/web public", got[1])
	}
}

// The envelope is the trap here: unlike /user/repos this endpoint wraps the page
// in an object, so a full page must be recognized as "there may be more" and the
// walk must stop on the short one.
func TestListInstallationReposPaginates(t *testing.T) {
	var pages []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		// Page 1 comes back full (100), so the walk must ask for page 2; page 2
		// is short, so it must stop there.
		repos := []githubapi.Repo{{FullName: "acme/last"}}
		if page == "1" {
			repos = make([]githubapi.Repo, 0, 100)
			for i := range 100 {
				repos = append(repos, githubapi.Repo{FullName: fmt.Sprintf("acme/r%d", i)})
			}
		}
		writeJSON(t, w, reposPage{TotalCount: 101, Repositories: repos})
	}))
	defer ts.Close()

	got, err := appClient(t, ts, testKey(t)).ListInstallationRepos(t.Context(), testUserToken, testInstallationID)
	if err != nil {
		t.Fatalf("ListInstallationRepos: %v", err)
	}

	if len(got) != 101 {
		t.Errorf("len(repos) = %d, want 101 (a full page plus a short one)", len(got))
	}
	if len(pages) != 2 || pages[0] != "1" || pages[1] != "2" {
		t.Errorf("pages requested = %v, want [1 2]", pages)
	}
}

// An empty installation is a real answer — somebody can install the App and tick
// nothing — so it must be an empty slice, not an error and not nil.
func TestListInstallationReposEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, reposPage{TotalCount: 0, Repositories: []githubapi.Repo{}})
	}))
	defer ts.Close()

	got, err := appClient(t, ts, testKey(t)).ListInstallationRepos(t.Context(), testUserToken, testInstallationID)
	if err != nil {
		t.Fatalf("ListInstallationRepos: %v", err)
	}
	if got == nil {
		t.Fatal("repos = nil, want an empty slice")
	}
	if len(got) != 0 {
		t.Errorf("len(repos) = %d, want 0", len(got))
	}
}

// A dead or under-privileged token means "re-authorize", matching ListRepos'
// contract so the picker's caller handles one sentinel, not two.
func TestListInstallationReposUnauthorized(t *testing.T) {
	for _, status := range []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
	} {
		t.Run(fmt.Sprintf("http %d", status), func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			defer ts.Close()

			_, err := appClient(t, ts, testKey(t)).ListInstallationRepos(
				t.Context(), testUserToken, testInstallationID,
			)
			if !errors.Is(err, githubapi.ErrUnauthorized) {
				t.Errorf("error = %v, want ErrUnauthorized", err)
			}
		})
	}
}

// A 5xx on the listing is an outage, not a re-authorize prompt — the same
// inversion the mint guards against.
func TestListInstallationReposServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := appClient(t, ts, testKey(t)).ListInstallationRepos(t.Context(), testUserToken, testInstallationID)
	if !errors.Is(err, githubapi.ErrListRepos) {
		t.Fatalf("error = %v, want ErrListRepos", err)
	}
	if errors.Is(err, githubapi.ErrUnauthorized) {
		t.Errorf("error = %v, must NOT be ErrUnauthorized on a 5xx", err)
	}
}

// jwtClaims is the subset of the app JWT's payload GitHub validates.
type jwtClaims struct {
	Issuer    string `json:"iss"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

// decodeJWTClaims pulls the claims out of a signed JWT WITHOUT verifying it —
// the test asserts what Kiln sent, and re-verifying with the same library that
// signed it would only prove the library is self-consistent.
func decodeJWTClaims(t *testing.T, token string) jwtClaims {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3 (header.claims.signature)", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode jwt claims segment: %v", err)
	}
	var claims jwtClaims
	if jerr := json.Unmarshal(payload, &claims); jerr != nil {
		t.Fatalf("unmarshal jwt claims: %v", jerr)
	}
	return claims
}

// verifyRS256 checks a JWT signature the way GitHub would: SHA-256 over the
// signing input, RSA PKCS#1 v1.5 against the App's public key.
func verifyRS256(pub *rsa.PublicKey, signingInput string, sig []byte) error {
	sum := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return fmt.Errorf("verify RS256: %w", err)
	}
	return nil
}
