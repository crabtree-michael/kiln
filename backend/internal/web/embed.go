// Package web embeds the built frontend SPA into the Go binary and serves it
// same-origin with the API (design 2026-07-05, decision e/D2): the client and
// server share one Render web service, so the board's SSE stream runs on the
// same origin and no rewrite-proxy sits in front of it.
//
// The dist directory is a committed placeholder (index.html) so `go build ./...`
// stays green locally; the Docker build overwrites dist with the real
// `pnpm build` output before the Go stage embeds and compiles.
package web

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler serves the SPA embedded into the binary at build time. It is the
// production entry point; HandlerFS is the same logic over an arbitrary dist
// tree, which the tests use to simulate successive deploys (each with a
// disjoint set of hashed asset filenames).
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Only possible if the embed directive itself is broken — a build-time
		// invariant, so failing loudly at construction is correct.
		panic("web: sub dist: " + err.Error())
	}
	return HandlerFS(sub)
}

// HandlerFS serves the SPA out of fsys: an existing file (hashed JS/CSS under
// /assets, images, the manifest) is served directly by the file server; every
// other path falls back to index.html so client-side routes ("/", "/debug")
// render. It is mounted as the api mux's "/" catch-all, so /api/* and /healthz
// are matched by their own patterns first; the explicit guard here is
// belt-and-suspenders.
//
// Caching policy is split by path so a deploy never strands a client:
//   - /assets/* are content-hashed by Vite (the hash is the identity of the
//     bytes), so they are served `immutable` with a one-year max-age. A client
//     that already fetched a given hash never revalidates it, and — crucially —
//     the filename changing on every content change is what makes the honest
//     404 below the *only* way a superseded asset is ever missing.
//   - index.html and every other root file (manifest, the self-destroying SW,
//     favicon) are served `no-cache`: they keep the same name across deploys, so
//     the client must revalidate to pick up new asset references. Serving a
//     stale shell is precisely what stranded mobile clients on dead hashes.
func HandlerFS(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		panic("web: read embedded index.html: " + err.Error())
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			http.NotFound(w, r)
			return
		}
		upath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if upath != "" && isFile(fsys, upath) {
			if strings.HasPrefix(upath, "assets/") {
				// Content-hashed and immutable: safe to cache aggressively, and
				// doing so keeps the client off the network for assets it already
				// holds (fewer chances to race a deploy mid-load).
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				// Stable-named root files change per deploy — must revalidate.
				w.Header().Set("Cache-Control", "no-cache")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		// A path that carries a file extension but resolved to no embedded file is
		// a stale/renamed static asset, not a client route — almost always a
		// previously-cached client (browser HTTP cache or an old service-worker
		// precache) requesting a hashed bundle from a superseded deploy. Answer an
		// honest 404 rather than the HTML shell: serving index.html in place of a
		// missing `.css`/`.js` makes the browser reject it on MIME mismatch and
		// silently drop the stylesheet — the page renders fully unstyled — or fail
		// the entry module script outright. A 404 lets the client/SW fail cleanly
		// and re-fetch the current shell. Client routes ("/", "/debug") are
		// extensionless, so they still fall through to the SPA entry below.
		if path.Ext(upath) != "" {
			http.NotFound(w, r)
			return
		}
		// Root and any unknown extensionless (client-route) path resolve to the
		// SPA entry point.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if _, err := w.Write(index); err != nil {
			slog.DebugContext(r.Context(), "web: write index.html", "err", err)
		}
	})
}

// isFile reports whether name resolves to a regular embedded file (not a
// directory and not missing) — the test that separates a real asset request from
// a client-route path that must fall through to index.html.
func isFile(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}
