// Package api is the client-facing surface (04 §7): the HTTP routes and the
// SSE hub. Transport: SSE for server→client, plain HTTP POST for
// client→server (04 D6). Handlers are thin — decode, delegate to
// runtime/board, encode (02 §2); request/response and SSE-payload shapes
// live in /schema, never hand-written here.
//
// Auth on these endpoints is deferred to 02 §12 (single-user; v1 is
// local-only).
package api
