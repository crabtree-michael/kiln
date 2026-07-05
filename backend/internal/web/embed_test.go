package web_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/crabtree-michael/kiln/backend/internal/web"
)

// deployFS builds a synthetic dist tree for one deploy: an index.html shell that
// references a single content-hashed JS+CSS pair under /assets, plus those two
// asset files. Two deploys built with different hashes share no asset filenames
// — exactly the zero-overlap rollover that stranded mobile clients — so a
// handler over deployFS(next) can be probed with references from deployFS(prev).
func deployFS(hash string) fstest.MapFS {
	shell := "<!doctype html>\n<html><head>" +
		`<link rel="stylesheet" href="/assets/index-` + hash + `.css">` +
		`<script type="module" src="/assets/index-` + hash + `.js"></script>` +
		"</head><body><div id=\"root\"></div></body></html>"
	return fstest.MapFS{
		"index.html":                    {Data: []byte(shell)},
		"assets/index-" + hash + ".css": {Data: []byte("body{color:red}")},
		"assets/index-" + hash + ".js":  {Data: []byte("export const x=1")},
		"manifest.webmanifest":          {Data: []byte(`{"name":"Kiln"}`)},
	}
}

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

// TestHandler_DeployRolloverStrandsNoClient is the recurrence guard for the
// zero-overlap asset window. It stands up the handler for a *new* deploy and
// probes it exactly as a client left holding the *previous* deploy's shell would:
//
//   - the previous deploy's hashed assets 404 (honest failure, never the shell),
//     which is what the client-side recovery listens for to reload; and
//   - re-fetching the shell yields the NEW deploy's shell (referencing the new
//     hashes) and is served no-cache, so the client that reloads lands on assets
//     that actually resolve — closing the loop instead of re-stranding it.
func TestHandler_DeployRolloverStrandsNoClient(t *testing.T) {
	prev, next := deployFS("AAAAAAAA"), deployFS("BBBBBBBB")
	ts := httptest.NewServer(web.HandlerFS(next))
	defer ts.Close()

	// A client holding the previous shell requests its (now-superseded) assets.
	for _, asset := range []string{"/assets/index-AAAAAAAA.css", "/assets/index-AAAAAAAA.js"} {
		resp := getURL(t, ts.URL+asset)
		body := readAll(t, resp)
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("stale asset %s status = %d, want 404 (must not serve the shell)", asset, resp.StatusCode)
		}
		if strings.Contains(body, "<!doctype html>") {
			t.Errorf("stale asset %s returned the HTML shell; body=%q", asset, body)
		}
	}

	// The recovery reload re-fetches the shell: it must be the NEW deploy's shell
	// (new hashes) and must be revalidated every time, never cached.
	resp := getURL(t, ts.URL+"/")
	shell := readAll(t, resp)
	cc := resp.Header.Get("Cache-Control")
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
	if !strings.Contains(shell, "index-BBBBBBBB.js") {
		t.Errorf("shell did not reference the new deploy's assets; body=%q", shell)
	}
	if strings.Contains(shell, "index-AAAAAAAA") {
		t.Errorf("shell still references the previous deploy's assets; body=%q", shell)
	}
	if !strings.Contains(cc, "no-cache") {
		t.Errorf("shell Cache-Control = %q, want no-cache (must revalidate to pick up new hashes)", cc)
	}
	// Sanity: the new deploy's assets resolve for the reloaded client.
	got := getURL(t, ts.URL+"/assets/index-BBBBBBBB.js")
	if err := got.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
	if got.StatusCode != http.StatusOK {
		t.Errorf("new asset status = %d, want 200", got.StatusCode)
	}
	_ = prev // prev documents the client's origin deploy; the handler serves next.
}

// TestHandler_ImmutableCachesHashedAssets confirms content-hashed assets are
// served with a long-lived immutable Cache-Control. This is safe only because a
// hash change renames the file (new URL, fresh fetch) and a missing hash 404s
// honestly — both guaranteed by the tests above — so a cached-forever asset is
// never a stale asset.
func TestHandler_ImmutableCachesHashedAssets(t *testing.T) {
	ts := httptest.NewServer(web.HandlerFS(deployFS("CAFEF00D")))
	defer ts.Close()

	resp := getURL(t, ts.URL+"/assets/index-CAFEF00D.css")
	cc := resp.Header.Get("Cache-Control")
	ct := resp.Header.Get("Content-Type")
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hashed asset status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(cc, "immutable") || !strings.Contains(cc, "max-age=31536000") {
		t.Errorf("asset Cache-Control = %q, want public, max-age=31536000, immutable", cc)
	}
	if !strings.Contains(ct, "text/css") {
		t.Errorf("asset Content-Type = %q, want text/css", ct)
	}

	// A root file that keeps its name across deploys must NOT be immutable-cached.
	m := getURL(t, ts.URL+"/manifest.webmanifest")
	mcc := m.Header.Get("Cache-Control")
	if err := m.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
	if strings.Contains(mcc, "immutable") {
		t.Errorf("manifest Cache-Control = %q, must not be immutable (stable name, changes per deploy)", mcc)
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
