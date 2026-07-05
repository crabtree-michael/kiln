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

// Handler serves the embedded SPA: an existing embedded file (hashed JS/CSS,
// images, the manifest) is served directly by the file server; every other path
// falls back to index.html so client-side routes ("/", "/debug") render. It is
// mounted as the api mux's "/" catch-all, so /api/* and /healthz are matched by
// their own patterns first; the explicit guard here is belt-and-suspenders.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Only possible if the embed directive itself is broken — a build-time
		// invariant, so failing loudly at construction is correct.
		panic("web: sub dist: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic("web: read embedded index.html: " + err.Error())
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			http.NotFound(w, r)
			return
		}
		if upath := strings.TrimPrefix(path.Clean(r.URL.Path), "/"); upath != "" && isFile(sub, upath) {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Root and any unknown non-asset path resolve to the SPA entry point.
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
