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

const (
	testClientID     = "client-123"
	testClientSecret = "secret-xyz"
)

func TestAuthorizeURL(t *testing.T) {
	c := githubapi.New(githubapi.Config{
		ClientID:     testClientID,
		ClientSecret: testClientSecret,
		OAuthBaseURL: "https://github.example",
	}, nil)

	got := c.AuthorizeURL("state-abc")

	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse AuthorizeURL result: %v", err)
	}
	if u.Path != "/login/oauth/authorize" {
		t.Errorf("path = %q, want /login/oauth/authorize", u.Path)
	}
	q := u.Query()
	if q.Get("client_id") != testClientID {
		t.Errorf("client_id = %q, want client-123", q.Get("client_id"))
	}
	if q.Get("state") != "state-abc" {
		t.Errorf("state = %q, want state-abc", q.Get("state"))
	}
	if q.Has("scope") {
		t.Errorf("expected no scope param, got %q", q.Get("scope"))
	}
	if !strings.HasPrefix(got, "https://github.example/") {
		t.Errorf("AuthorizeURL = %q, want prefix https://github.example/", got)
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
		if _, werr := w.Write([]byte(`{"access_token":"gho_x","token_type":"bearer"}`)); werr != nil {
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
