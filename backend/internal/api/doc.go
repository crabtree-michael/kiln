// Package api is the client-facing surface (04 §7): the HTTP routes and the
// SSE hub. Transport: SSE for server→client, plain HTTP POST for
// client→server (04 D6). Handlers are thin — decode, delegate to
// runtime/board, encode (02 §2); request/response and SSE-payload shapes
// live in /schema, never hand-written here.
//
// # Auth
//
// Callers are authenticated, not assumed. Sign-in is GitHub OAuth, and the
// session it mints rides an HttpOnly cookie (11 §2); session.go owns the
// cookie plumbing so the HttpOnly/SameSite/Secure flags cannot drift between
// call sites. withSession is the guard every protected route wraps — the
// account surface (/api/me, settings, push, voice) directly, and the app
// surface through the project-scoping wrappers below.
//
// # Tenancy
//
// The server is multi-tenant: users own projects, and a request only ever
// reaches the board of a project its session authorizes (11 §3, 11 §6). Every
// app route is dual-mounted (12 §3.2) — bare (/api/board), scoped by
// withProject to the caller's current project, and id'd
// (/api/projects/{pid}/board), scoped by withProjectID to the named
// owner-authorized one. The ports this package holds (BoardReader and friends)
// are project-scoped for the same reason, and the hub fans out per project so
// one tenant's stream never carries another's events.
//
// # Direct board writes
//
// D5 — "the client never mutates the board directly" — holds for transitions:
// accept and delete route through the brain. Three narrow exceptions write the
// board without a brain pass, each behind its own port so the exception stays
// visible rather than widening BoardReader: the per-ticket sandbox controls,
// the detail sheet's ticket-text edit, and ticket dependencies. See the port
// declarations in routes.go for why each one earns the exception.
package api
