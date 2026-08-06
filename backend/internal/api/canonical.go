package api

// The canonical-origin redirect (11 §2, added 2026-08-06 by the second sign-in
// fix): every browser navigation is pulled onto ONE public origin, whichever
// host it arrived on.
//
// It exists because a cookie belongs to a host and the GitHub App's callback URL
// does not. The state cookie is written when the browser asks for
// /auth/github/connect — on whatever host the user is reading Kiln on — and
// GitHub then delivers the callback to the App's single registered callback URL,
// which is a fixed string in GitHub's settings and has no idea where the user
// started. A deployment reachable at both its platform hostname and its real
// domain therefore splits the flow across two cookie jars: state written on one
// host, read on the other, found on neither. The user sees "missing oauth state
// cookie", and had the check passed they would have been handed a session cookie
// on the wrong domain and left living on the platform URL.
//
// Pulling the callback back onto the canonical host before it is handled fixes
// both halves at once, because a redirect carries `code` and `state` in the
// query untouched and the state cookie is waiting on the other side. It also
// means the App's registered callback URL no longer has to be the domain users
// actually type — either host now ends the flow in the same place.

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ErrPublicURL is the static base for a KILN_PUBLIC_URL that is set but is not
// an absolute http(s) URL with a host. Set-but-malformed is a hard error rather
// than a shrug: the value exists solely to make sign-in land somewhere, and a
// deployment that silently ignored it would serve the exact breakage it was
// added to fix, with the operator believing it configured.
var ErrPublicURL = errors.New("api: public url")

// healthzPath is the liveness route, named once because the canonical redirect
// has to exempt it: the platform's health check reaches the process on an
// internal hostname, and a 302 away from it reads as an unhealthy instance.
const healthzPath = "/healthz"

// canonicalOrigin is the one public origin browser navigations are pulled onto
// — scheme and host, never a path. Kiln is served at the root of its domain, so
// a base path would be a shape the rest of the surface does not have.
type canonicalOrigin struct {
	scheme string
	host   string
}

// EnableCanonicalHost pins the deployment's public origin (call before Handler):
// a GET/HEAD that arrives on any other host is redirected to the same path and
// query on this one. Left unset — local dev, tests, any single-host deployment —
// no redirect happens at all and the handler chain is the bare mux.
//
// publicURL is the origin users type (e.g. "https://trykiln.dev"); anything
// beyond scheme and host is ignored. It returns ErrPublicURL for a value that is
// not an absolute http(s) URL with a host, which the composition root treats as
// a fatal misconfiguration.
func (s *Server) EnableCanonicalHost(publicURL string) error {
	u, err := url.Parse(strings.TrimSpace(publicURL))
	if err != nil {
		return fmt.Errorf("%w: parse %q: %w", ErrPublicURL, publicURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: %q needs an http:// or https:// scheme", ErrPublicURL, publicURL)
	}
	if u.Host == "" {
		return fmt.Errorf("%w: %q names no host", ErrPublicURL, publicURL)
	}
	s.canonical = &canonicalOrigin{scheme: u.Scheme, host: u.Host}
	return nil
}

// withCanonicalHost wraps the mux so an off-origin navigation is redirected
// before any handler runs — in particular before the callback reads a state
// cookie that only exists on the canonical host.
//
// A no-op when no origin was pinned: the wrapper is not even built, so a
// single-host deployment pays nothing.
func (s *Server) withCanonicalHost(next http.Handler) http.Handler {
	if s.canonical == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.offCanonicalHost(r) {
			next.ServeHTTP(w, r)
			return
		}
		// Only the scheme and host are replaced; the path and query ride across
		// untouched, which is what lets the GitHub callback keep its `code` and
		// `state` through the hop.
		target := *r.URL
		target.Scheme = s.canonical.scheme
		target.Host = s.canonical.host
		// 302, not 301: a permanent redirect is cached by the browser for as
		// long as it likes, and an origin an operator may need to correct must
		// not be baked into every user's cache to do it.
		//nolint:gosec // G710: not an open redirect — the destination's origin is
		// the operator's KILN_PUBLIC_URL, and only the path/query come from the
		// request, so a caller can pick where on Kiln it lands but never which host.
		http.Redirect(w, r, target.String(), http.StatusFound)
	})
}

// offCanonicalHost reports whether this request should be sent to the canonical
// origin instead of served here.
//
// Only GET and HEAD. A redirected POST is a request whose body the browser may
// not resend and whose method it may not preserve, and every write in this API
// is issued by a page that has already been pulled onto the canonical origin —
// so there is nothing to gain and a silently dropped write to lose.
//
// /healthz is exempt (see healthzPath).
func (s *Server) offCanonicalHost(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.URL.Path == healthzPath {
		return false
	}
	// Case-insensitive, because hostnames are. The port is compared along with
	// the name even though cookie jars ignore ports: the job here is to land
	// users on the one URL the deployment is published at, and an origin that
	// named a port meant it.
	return !strings.EqualFold(r.Host, s.canonical.host)
}
