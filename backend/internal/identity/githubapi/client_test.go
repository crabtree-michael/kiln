package githubapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/identity/githubapi"
)

// Shared across this file and app_test.go — one spelling each, so a test that
// asserts on them cannot drift from the one that sends them.
const (
	testClientID     = "client-123"
	testClientSecret = "secret-xyz"
	// testGitHubHost stands in for github.com wherever a test asserts on URL
	// building rather than on a round trip through httptest.
	testGitHubHost = "https://github.example"
	testState      = "state-abc"
	testRepoName   = "acme/api"
	testRepoURL    = "https://github.com/acme/api"
)

// The sign-in redirect. It has to be the AUTHORIZE endpoint, carrying the App's
// client id and the state nonce: this is the one URL GitHub answers with a code
// no matter how many times a given account has been through it, which is the
// whole reason sign-in stopped starting at the install page.
func TestAuthorizeURL(t *testing.T) {
	c := githubapi.New(githubapi.Config{
		ClientID:     testClientID,
		OAuthBaseURL: testGitHubHost,
	}, nil)

	got := c.AuthorizeURL(testState)

	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse AuthorizeURL result: %v", err)
	}
	if u.Path != "/login/oauth/authorize" {
		t.Errorf("path = %q, want /login/oauth/authorize", u.Path)
	}
	if u.Query().Get("client_id") != testClientID {
		t.Errorf("client_id = %q, want %q", u.Query().Get("client_id"), testClientID)
	}
	if u.Query().Get("state") != testState {
		t.Errorf("state = %q, want %q", u.Query().Get("state"), testState)
	}
	if !strings.HasPrefix(got, testGitHubHost+"/") {
		t.Errorf("AuthorizeURL = %q, want prefix %s/", got, testGitHubHost)
	}
}

func TestExchangeCodeSuccess(t *testing.T) {
	var gotMethod, gotPath, gotAccept string
	var gotBody struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		Code         string `json:"code"`
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAccept = r.Header.Get("Accept")
		if derr := json.NewDecoder(r.Body).Decode(&gotBody); derr != nil {
			t.Errorf("decode request body: %v", derr)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, werr := w.Write([]byte(`{"access_token":"gho_x","scope":"repo","token_type":"bearer"}`)); werr != nil {
			t.Errorf("write response: %v", werr)
		}
	}))
	defer ts.Close()

	c := githubapi.New(githubapi.Config{
		ClientID:     testClientID,
		ClientSecret: testClientSecret,
		OAuthBaseURL: ts.URL,
	}, nil)

	tok, err := c.ExchangeCode(context.Background(), "code-abc")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tok != "gho_x" {
		t.Errorf("token = %q, want gho_x", tok)
	}
	if gotMethod != http.MethodPost || gotPath != "/login/oauth/access_token" {
		t.Errorf("request = %s %s, want POST /login/oauth/access_token", gotMethod, gotPath)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want application/json", gotAccept)
	}
	if gotBody.ClientID != testClientID || gotBody.ClientSecret != testClientSecret || gotBody.Code != "code-abc" {
		t.Errorf("body = %+v, want client_id/client_secret/code populated", gotBody)
	}
}

func TestExchangeCodeOAuthError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := `{"error":"bad_verification_code",` +
			`"error_description":"The code passed is incorrect or expired."}`
		if _, werr := w.Write([]byte(body)); werr != nil {
			t.Errorf("write response: %v", werr)
		}
	}))
	defer ts.Close()

	c := githubapi.New(githubapi.Config{
		ClientID:     testClientID,
		ClientSecret: testClientSecret,
		OAuthBaseURL: ts.URL,
	}, nil)

	_, err := c.ExchangeCode(context.Background(), "code-abc")
	if err == nil {
		t.Fatal("expected error on OAuth error body, got nil")
	}
	if !errors.Is(err, githubapi.ErrExchange) {
		t.Errorf("error = %v, want wrapping ErrExchange", err)
	}
	if !strings.Contains(err.Error(), "The code passed is incorrect or expired.") {
		t.Errorf("error = %v, want it to contain the error_description", err)
	}
}

func TestExchangeCodeHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	c := githubapi.New(githubapi.Config{
		ClientID:     testClientID,
		ClientSecret: testClientSecret,
		OAuthBaseURL: ts.URL,
	}, nil)

	_, err := c.ExchangeCode(context.Background(), "code-abc")
	if err == nil {
		t.Fatal("expected error on 503, got nil")
	}
	if !errors.Is(err, githubapi.ErrExchange) {
		t.Errorf("error = %v, want wrapping ErrExchange", err)
	}
}

func TestFetchUserSuccess(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotAccept string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		body := `{"id":123,"login":"Crabtree-Michael","name":"Michael",` +
			`"avatar_url":"https://example.com/a.png"}`
		if _, werr := w.Write([]byte(body)); werr != nil {
			t.Errorf("write response: %v", werr)
		}
	}))
	defer ts.Close()

	c := githubapi.New(githubapi.Config{APIBaseURL: ts.URL}, nil)

	u, err := c.FetchUser(context.Background(), "gho_x")
	if err != nil {
		t.Fatalf("FetchUser: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/user" {
		t.Errorf("request = %s %s, want GET /user", gotMethod, gotPath)
	}
	if gotAuth != "Bearer gho_x" {
		t.Errorf("Authorization = %q, want Bearer gho_x", gotAuth)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("Accept = %q, want application/vnd.github+json", gotAccept)
	}
	want := githubapi.GitHubUser{
		ID: 123, Login: "Crabtree-Michael", Name: "Michael", AvatarURL: "https://example.com/a.png",
	}
	if u != want {
		t.Errorf("GitHubUser = %+v, want %+v", u, want)
	}
}

func TestFetchUserUnauthorized(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Bad credentials", http.StatusUnauthorized)
	}))
	defer ts.Close()

	c := githubapi.New(githubapi.Config{APIBaseURL: ts.URL}, nil)

	_, err := c.FetchUser(context.Background(), "gho_bad")
	if err == nil {
		t.Fatal("expected error on 401, got nil")
	}
	if !errors.Is(err, githubapi.ErrFetchUser) {
		t.Errorf("error = %v, want wrapping ErrFetchUser", err)
	}
}

func TestListReposSuccess(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotQuery url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		body := `[{"full_name":"acme/api","html_url":"https://github.com/acme/api","private":true},` +
			`{"full_name":"nobody/blog","html_url":"https://github.com/nobody/blog","private":false}]`
		if _, werr := w.Write([]byte(body)); werr != nil {
			t.Errorf("write response: %v", werr)
		}
	}))
	defer ts.Close()

	c := githubapi.New(githubapi.Config{APIBaseURL: ts.URL}, nil)

	repos, err := c.ListRepos(context.Background(), "gho_x")
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/user/repos" {
		t.Errorf("request = %s %s, want GET /user/repos", gotMethod, gotPath)
	}
	if gotAuth != "Bearer gho_x" {
		t.Errorf("Authorization = %q, want Bearer gho_x", gotAuth)
	}
	// Org repos the user merely collaborates on are the common real-world case,
	// so the affiliation filter must not narrow to owned repos.
	if got := gotQuery.Get("affiliation"); got != "owner,collaborator,organization_member" {
		t.Errorf("affiliation = %q, want owner,collaborator,organization_member", got)
	}
	if got := gotQuery.Get("per_page"); got != "100" {
		t.Errorf("per_page = %q, want 100", got)
	}
	want := []githubapi.Repo{
		{FullName: testRepoName, HTMLURL: testRepoURL, Private: true},
		{FullName: "nobody/blog", HTMLURL: "https://github.com/nobody/blog", Private: false},
	}
	if len(repos) != len(want) {
		t.Fatalf("ListRepos returned %d repos, want %d", len(repos), len(want))
	}
	for i := range want {
		if repos[i] != want[i] {
			t.Errorf("repos[%d] = %+v, want %+v", i, repos[i], want[i])
		}
	}
}

// A full page means "there may be more": the client must walk to the next page
// and stop at the first short one, so an account with more than 100 repos does
// not silently lose the tail of its list.
func TestListReposPaginates(t *testing.T) {
	var pages []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		w.Header().Set("Content-Type", "application/json")
		var batch []githubapi.Repo
		if page == "1" {
			batch = make([]githubapi.Repo, 100)
			for i := range batch {
				batch[i] = githubapi.Repo{FullName: "acme/full-page"}
			}
		} else {
			batch = []githubapi.Repo{{FullName: "acme/tail"}}
		}
		if werr := json.NewEncoder(w).Encode(batch); werr != nil {
			t.Errorf("encode response: %v", werr)
		}
	}))
	defer ts.Close()

	c := githubapi.New(githubapi.Config{APIBaseURL: ts.URL}, nil)

	repos, err := c.ListRepos(context.Background(), "gho_x")
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 101 {
		t.Errorf("len(repos) = %d, want 101", len(repos))
	}
	if strings.Join(pages, ",") != "1,2" {
		t.Errorf("requested pages = %v, want [1 2]", pages)
	}
}

// 403 is what GitHub answers when the token is valid but carries no repo scope —
// the exact state of every token minted before Kiln started asking for it. It
// must surface as ErrUnauthorized ("re-authorize"), never as a transport error.
func TestListReposMissingScopeIsUnauthorized(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "no", status)
		}))

		c := githubapi.New(githubapi.Config{APIBaseURL: ts.URL}, nil)
		_, err := c.ListRepos(context.Background(), "gho_stale")
		ts.Close()

		if !errors.Is(err, githubapi.ErrUnauthorized) {
			t.Errorf("http %d: error = %v, want wrapping ErrUnauthorized", status, err)
		}
		if !errors.Is(err, githubapi.ErrListRepos) {
			t.Errorf("http %d: error = %v, want wrapping ErrListRepos too", status, err)
		}
	}
}

func TestListReposServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := githubapi.New(githubapi.Config{APIBaseURL: ts.URL}, nil)

	_, err := c.ListRepos(context.Background(), "gho_x")
	if !errors.Is(err, githubapi.ErrListRepos) {
		t.Errorf("error = %v, want wrapping ErrListRepos", err)
	}
	// A plain outage is NOT a re-authorize prompt.
	if errors.Is(err, githubapi.ErrUnauthorized) {
		t.Errorf("error = %v, want it not to wrap ErrUnauthorized", err)
	}
}
