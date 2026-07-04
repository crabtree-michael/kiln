// Package obs holds shared, dependency-free observability helpers for the Kiln
// backend: a per-turn correlation id carried on the context and injected into
// every structured log record, plus small helpers for summarizing and hashing
// large payloads so brain deliveries and agent outputs are greppable by ticket
// id / turn id without dumping full instruction or diff text.
//
// The motivating failure is the suspected stuck/duplicated instruction delivery
// on ticket 841fb6cc — the agent re-confirming stale scope instead of receiving
// the latest instruction. Reconstructing a whole turn (trigger event → actions
// taken → result) from logs needs a correlation id threaded end-to-end and a
// stable content fingerprint that makes a redelivered stale instruction obvious.
//
// It imports only the standard library, so every module (board, brain, agent,
// runtime) may depend on it without crossing the layering rules the module docs
// state (no module imports another's internals).
package obs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
)

// TurnKey is the log attribute name every record carries when a turn id is set.
const TurnKey = "turn_id"

// hashHexLen is how many hex characters of the content digest a fingerprint
// keeps — short enough to eyeball, long enough that a collision between two
// distinct instructions is not a practical concern for debugging.
const hashHexLen = 12

// turnKey is the private context key under which the correlation/turn id rides.
type turnKey struct{}

// WithTurn returns a context carrying id as the correlation/turn id. Every slog
// record emitted with that context — through a logger built on Handler — gains
// a turn_id attribute, so one turn's whole lifecycle (trigger event, board
// mutations, delivery, result) is reconstructable by grepping turn_id.
func WithTurn(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, turnKey{}, id)
}

// TurnID returns the correlation/turn id on ctx, or "" if none is set.
func TurnID(ctx context.Context) string {
	id, ok := ctx.Value(turnKey{}).(string)
	if !ok {
		return ""
	}
	return id
}

// Handler wraps base so that every record whose context carries a turn id
// (WithTurn) gets a turn_id attribute automatically — no call site has to
// thread it. Install it once as the default logger at the composition root.
func Handler(base slog.Handler) slog.Handler {
	return &contextHandler{base: base}
}

// contextHandler is the turn-id-injecting slog.Handler returned by Handler.
type contextHandler struct{ base slog.Handler }

func (h *contextHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.base.Enabled(ctx, l)
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := TurnID(ctx); id != "" {
		r.AddAttrs(slog.String(TurnKey, id))
	}
	//nolint:wrapcheck // pass-through handler: the base handler's error is the contract.
	return h.base.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{base: h.base.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{base: h.base.WithGroup(name)}
}

// Hash returns a short, stable content fingerprint of s ("sha256:" + the first
// hashHexLen hex chars) so two deliveries of the same instruction — the
// stale-redelivery smell on ticket 841fb6cc — share a hash and are trivially
// greppable, without logging the full text.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])[:hashHexLen]
}

// Summary returns s unchanged when it is within maxBytes, otherwise its head
// and tail with an elision marker between them — enough to eyeball an
// instruction or agent output in a log line without carrying kilobytes of diff.
// Byte-based: the content is treated as opaque text, never re-parsed.
func Summary(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	const halves = 2
	half := maxBytes / halves
	return s[:half] + "…[truncated]…" + s[len(s)-half:]
}
