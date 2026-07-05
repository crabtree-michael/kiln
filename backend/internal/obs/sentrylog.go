package obs

// slog→Sentry-Logs bridge. NewLogger composes sentryHandler alongside the JSON
// stdout handler (via a fanout) only when Sentry is enabled, so structured
// records reach Sentry Logs carrying every attribute — including the turn_id the
// contextHandler injects — as Sentry log attributes. Disabled builds never
// construct it, so there is zero per-record cost locally.

import (
	"context"
	"errors"
	"log/slog"
	"math"

	"github.com/getsentry/sentry-go"
)

// fanoutHandler dispatches each record to every wrapped handler, respecting each
// one's own level via Enabled. It lets NewLogger send one record to both the
// JSON sink and the Sentry bridge without either seeing the other's mutations.
type fanoutHandler struct {
	handlers []slog.Handler
}

func newFanout(handlers ...slog.Handler) slog.Handler {
	return fanoutHandler{handlers: handlers}
}

func (f fanoutHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f.handlers {
		if h.Enabled(ctx, r.Level) {
			// Clone so one handler's attribute additions cannot leak into another.
			if err := h.Handle(ctx, r.Clone()); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (f fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithAttrs(attrs)
	}
	return fanoutHandler{handlers: hs}
}

func (f fanoutHandler) WithGroup(name string) slog.Handler {
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithGroup(name)
	}
	return fanoutHandler{handlers: hs}
}

// sentryHandler forwards slog records to Sentry Logs. It honors the same minimum
// level as the JSON sink (min) so debug noise is not shipped unless configured.
// Groups are flattened (attribute keys are used verbatim) — Kiln's records are
// flat key/value pairs, so no dotted-prefix handling is needed.
type sentryHandler struct {
	minLevel slog.Level
	attrs    []slog.Attr
}

func newSentryHandler(minLevel slog.Level) slog.Handler {
	return &sentryHandler{minLevel: minLevel}
}

func (h *sentryHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.minLevel
}

func (h *sentryHandler) Handle(ctx context.Context, r slog.Record) error {
	logger := sentry.NewLogger(ctx)
	entry := entryForLevel(logger, r.Level)
	for _, a := range h.attrs {
		entry = applySlogAttr(entry, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		entry = applySlogAttr(entry, a)
		return true
	})
	entry.Emit(r.Message)
	return nil
}

func (h *sentryHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)
	return &sentryHandler{minLevel: h.minLevel, attrs: merged}
}

func (h *sentryHandler) WithGroup(_ string) slog.Handler { return h }

// entryForLevel maps a slog.Level onto the matching Sentry LogEntry severity.
func entryForLevel(logger sentry.Logger, l slog.Level) sentry.LogEntry {
	switch {
	case l >= slog.LevelError:
		return logger.Error()
	case l >= slog.LevelWarn:
		return logger.Warn()
	case l >= slog.LevelInfo:
		return logger.Info()
	case l >= slog.LevelDebug:
		return logger.Debug()
	default:
		return logger.Trace()
	}
}

// applySlogAttr copies one slog attribute onto a Sentry LogEntry, typed where the
// Sentry attribute API has a matching setter and stringified otherwise (Duration,
// Time, errors, arbitrary Any values).
func applySlogAttr(e sentry.LogEntry, a slog.Attr) sentry.LogEntry {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return e.String(a.Key, v.String())
	case slog.KindInt64:
		return e.Int64(a.Key, v.Int64())
	case slog.KindUint64:
		return e.Int64(a.Key, uint64ToInt64(v.Uint64()))
	case slog.KindFloat64:
		return e.Float64(a.Key, v.Float64())
	case slog.KindBool:
		return e.Bool(a.Key, v.Bool())
	case slog.KindDuration, slog.KindTime, slog.KindAny, slog.KindGroup, slog.KindLogValuer:
		// Durations, timestamps, errors, arbitrary values, and (post-Resolve,
		// effectively unreached) groups/log-valuers stringify — the Sentry log
		// attribute API has no dedicated setter for them.
		return e.String(a.Key, v.String())
	default:
		return e.String(a.Key, v.String())
	}
}

// uint64ToInt64 saturates a uint64 into int64 so a value above math.MaxInt64
// cannot silently wrap negative when handed to the Sentry Int64 attribute
// setter (gosec G115).
func uint64ToInt64(u uint64) int64 {
	if u > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(u)
}
