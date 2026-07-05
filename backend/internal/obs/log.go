package obs

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// LogFileEnv names the optional durable-file sink: when set, structured logs
// are also appended to that path, independent of the container's stdout
// pipeline. Absent, logs go to stdout only (the existing pipeline).
const LogFileEnv = "KILN_LOG_FILE"

// LevelEnv names the minimum log level (debug|info|warn|error), default info.
// It mirrors the composition root's KILN_LOG_LEVEL so a single env var governs
// both the app logger and this instrumentation.
const LevelEnv = "KILN_LOG_LEVEL"

// logFilePerm is the mode for a freshly created KILN_LOG_FILE.
const logFilePerm os.FileMode = 0o644

// NewLogger builds the process's structured JSON-lines logger: stdout, plus the
// KILN_LOG_FILE append sink when set, at KILN_LOG_LEVEL (default info), with the
// turn-id context handler (Handler) installed so every record correlates.
//
// It always returns a usable logger; a failed file open is reported as a
// non-nil error while the returned logger still writes to stdout, so the caller
// can log the degradation and carry on rather than start blind. The sink is
// intentionally left open for the process lifetime — JSON records are written
// straight through (unbuffered), so nothing is lost at exit.
func NewLogger() (*slog.Logger, error) {
	level := parseLevel(os.Getenv(LevelEnv))
	opts := &slog.HandlerOptions{Level: level}

	path := os.Getenv(LogFileEnv)
	if path == "" {
		return slog.New(Handler(withSentry(slog.NewJSONHandler(os.Stdout, opts), level))), nil
	}

	// The path is operator configuration (KILN_LOG_FILE), not user input.
	//nolint:gosec // trusted env-configured log path
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFilePerm)
	if err != nil {
		stdout := slog.New(Handler(withSentry(slog.NewJSONHandler(os.Stdout, opts), level)))
		return stdout, fmt.Errorf("obs: open log file %q: %w", path, err)
	}
	w := io.MultiWriter(os.Stdout, f)
	return slog.New(Handler(withSentry(slog.NewJSONHandler(w, opts), level))), nil
}

// withSentry fans the JSON sink out to the Sentry-Logs bridge when Sentry is
// enabled (InitSentry must have run first), so structured records reach both
// stdout and Sentry Logs; the turn-id contextHandler still wraps the result, so
// turn_id rides to both. When Sentry is disabled it returns base unchanged —
// zero extra work per record locally.
func withSentry(base slog.Handler, level slog.Level) slog.Handler {
	if !SentryEnabled() {
		return base
	}
	return newFanout(base, newSentryHandler(level))
}

// parseLevel maps a KILN_LOG_LEVEL string to a slog.Level, defaulting to info
// for empty or unrecognized values.
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
