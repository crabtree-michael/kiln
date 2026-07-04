package assemblyai_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/voice/assemblyai"
)

func TestMintStreamingToken_HappyPath(t *testing.T) {
	var gotAuth, gotQuery, gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.Query().Get("expires_in_seconds")
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		if _, werr := w.Write([]byte(`{"token":"tok-abc","expires_in_seconds":480}`)); werr != nil {
			t.Errorf("write response: %v", werr)
		}
	}))
	defer ts.Close()

	c := assemblyai.New(assemblyai.Config{APIKey: "key-123", BaseURL: ts.URL, TTL: 480 * time.Second})
	before := time.Now()
	tok, exp, err := c.MintStreamingToken(context.Background())
	if err != nil {
		t.Fatalf("MintStreamingToken: %v", err)
	}
	if tok != "tok-abc" {
		t.Errorf("token = %q, want tok-abc", tok)
	}
	if gotMethod != http.MethodGet || gotPath != "/v3/token" {
		t.Errorf("request = %s %s, want GET /v3/token", gotMethod, gotPath)
	}
	if gotAuth != "key-123" {
		t.Errorf("Authorization = %q, want key-123", gotAuth)
	}
	if gotQuery != "480" {
		t.Errorf("expires_in_seconds = %q, want 480", gotQuery)
	}
	if exp.Before(before.Add(470*time.Second)) || exp.After(time.Now().Add(490*time.Second)) {
		t.Errorf("expiresAt = %v, not ~now+480s", exp)
	}
}

func TestMintStreamingToken_ProviderError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer ts.Close()
	c := assemblyai.New(assemblyai.Config{APIKey: "bad", BaseURL: ts.URL, TTL: 480 * time.Second})
	if _, _, err := c.MintStreamingToken(context.Background()); err == nil {
		t.Fatal("expected error on 401, got nil")
	}
}

func TestMintStreamingToken_NoToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, werr := w.Write([]byte(`{"expires_in_seconds":480}`)); werr != nil {
			t.Errorf("write response: %v", werr)
		}
	}))
	defer ts.Close()
	c := assemblyai.New(assemblyai.Config{APIKey: "k", BaseURL: ts.URL, TTL: 480 * time.Second})
	if _, _, err := c.MintStreamingToken(context.Background()); err == nil {
		t.Fatal("expected error on empty token, got nil")
	}
}
