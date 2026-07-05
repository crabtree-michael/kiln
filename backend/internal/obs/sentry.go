package obs

// Sentry wiring for the Kiln backend: error/panic capture, tracing spans, and a
// slog→Sentry-Logs bridge. Everything here degrades to a no-op when
// SENTRY_BACKEND_DSN is unset — InitSentry returns an empty flush func, spans
// collapse to the bare context, Capture only re-logs, and the slog bridge is
// never composed (see NewLogger). Local `make up` and `go test` are therefore
// unaffected by this package.
//
// This is the one file in obs that reaches beyond the standard library
// (github.com/getsentry/sentry-go); the rest of the package stays dependency-free
// so every module can keep importing obs for turn-id/log helpers.

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/getsentry/sentry-go"
)

// flushTimeout bounds how long a deferred Flush waits for the transport to
// drain buffered events/logs at shutdown (matches sentry's default).
const flushTimeout = 2 * time.Second

// sentryEnabled is set once by a successful InitSentry. It gates every
// Sentry-touching path so a disabled build pays nothing beyond a bool check.
var sentryEnabled bool

// SentryConfig is InitSentry's input: the backend DSN (empty ⇒ disabled), the
// deployment environment string, and the release (main.version).
type SentryConfig struct {
	DSN         string
	Environment string
	Release     string
}

// InitSentry initializes the process-wide Sentry client from cfg and returns a
// flush func to defer at the composition root. When cfg.DSN is empty (local,
// tests) it is a no-op returning an empty func; a genuine init failure is logged
// and also degrades to no-op rather than aborting startup. Logs and tracing are
// enabled (one user → TracesSampleRate 1.0); logs ride the same DSN.
func InitSentry(cfg SentryConfig) func() {
	if cfg.DSN == "" {
		return func() {}
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              cfg.DSN,
		Environment:      cfg.Environment,
		Release:          cfg.Release,
		EnableTracing:    true,
		TracesSampleRate: 1.0,
		// Logs are enabled by default (DisableLogs stays false); the slog bridge
		// in NewLogger forwards structured records to Sentry Logs.
	})
	if err != nil {
		slog.Error("obs: sentry init failed; continuing without Sentry", "err", err)
		return func() {}
	}
	sentryEnabled = true
	return func() { sentry.Flush(flushTimeout) }
}

// SentryEnabled reports whether InitSentry successfully configured a client.
func SentryEnabled() bool { return sentryEnabled }

// StartSpan opens a tracing span named op with description desc around a unit of
// background work (brain dispatch, outbox delivery). It returns a child context
// to pass down and a finish func to defer. When Sentry is disabled it returns
// ctx unchanged and a no-op finish, so call sites need no conditional.
func StartSpan(ctx context.Context, op, desc string) (context.Context, func()) {
	if !sentryEnabled {
		return ctx, func() {}
	}
	span := sentry.StartSpan(ctx, op, sentry.WithDescription(desc))
	return span.Context(), span.Finish
}

// Capture reports a recovered panic value to Sentry and re-logs it with a stack
// trace. It does NOT itself recover — the caller's deferred recover() must have
// already fired and pass the value in — so a panicking worker reports instead of
// silently taking the process down. When Sentry is disabled it only re-logs.
func Capture(ctx context.Context, panicVal any) {
	slog.ErrorContext(ctx, "panic recovered",
		"panic", fmt.Sprintf("%v", panicVal), "stack", string(debug.Stack()))
	if sentryEnabled {
		hub := sentry.GetHubFromContext(ctx)
		if hub == nil {
			hub = sentry.CurrentHub()
		}
		hub.RecoverWithContext(ctx, panicVal)
	}
}
