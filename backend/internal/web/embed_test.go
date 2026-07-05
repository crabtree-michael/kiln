package web_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/web"
)

// TestHandler_ServesEmbeddedIndex confirms the committed placeholder dist embeds
// and the handler serves index.html for a client route — the invariant that keeps
// `go build ./...` green before a real frontend build is copied in.
func TestHandler_ServesEmbeddedIndex(t *testing.T) {
	ts := httptest.NewServer(web.Handler())
	defer ts.Close()

	for _, path := range []string{"/", "/debug"} {
		resp := getURL(t, ts.URL+path)
		body := readAll(t, resp)
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, resp.StatusCode)
		}
		if !strings.Contains(body, "<!doctype html>") {
			t.Errorf("GET %s did not return the SPA shell; body=%q", path, body)
		}
	}
}

// TestHandler_404sMissingHashedAsset is the stale-cache regression guard: a
// request for a hashed bundle that no longer exists after a deploy must 404, not
// fall through to the HTML shell. Serving index.html as a `.css`/`.js` response
// makes a previously-cached client reject it on MIME mismatch and render
// unstyled; an honest 404 lets it fail cleanly and re-fetch the current shell.
func TestHandler_404sMissingHashedAsset(t *testing.T) {
	ts := httptest.NewServer(web.Handler())
	defer ts.Close()

	for _, path := range []string{
		"/assets/index-OLDHASH0.css",
		"/assets/index-DEADBEEF.js",
		"/favicon-gone.ico",
	} {
		resp := getURL(t, ts.URL+path)
		body := readAll(t, resp)
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404 (missing asset must not serve the shell)", path, resp.StatusCode)
		}
		if strings.Contains(body, "<!doctype html>") {
			t.Errorf("GET %s returned the HTML shell for a missing asset; body=%q", path, body)
		}
	}
}

// TestHandler_GuardsAPIAndHealthz confirms the SPA never answers API/health probes
// even if it is somehow reached directly (belt-and-suspenders to the mux ordering).
func TestHandler_GuardsAPIAndHealthz(t *testing.T) {
	ts := httptest.NewServer(web.Handler())
	defer ts.Close()

	for _, path := range []string{"/api/board", "/healthz"} {
		resp := getURL(t, ts.URL+path)
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404 (SPA must not shadow API/health)", path, resp.StatusCode)
		}
	}
}

// getURL issues a context-ful GET and fails the test on a transport error.
func getURL(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build GET %s: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// readAll reads the response body; the caller owns closing it (so bodyclose can
// see the Close in the test's own scope).
func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
