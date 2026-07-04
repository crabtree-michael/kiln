package obs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/obs"
)

func TestTurnIDRoundTrip(t *testing.T) {
	t.Parallel()
	if got := obs.TurnID(context.Background()); got != "" {
		t.Fatalf("TurnID on a bare context = %q, want empty", got)
	}
	ctx := obs.WithTurn(context.Background(), "evt-42")
	if got := obs.TurnID(ctx); got != "evt-42" {
		t.Fatalf("TurnID = %q, want evt-42", got)
	}
}

func TestHashStableAndDistinct(t *testing.T) {
	t.Parallel()
	a := obs.Hash("do the thing")
	if a != obs.Hash("do the thing") {
		t.Fatal("Hash is not stable for equal input")
	}
	if !strings.HasPrefix(a, "sha256:") {
		t.Fatalf("Hash %q lacks sha256: prefix", a)
	}
	// A different instruction must fingerprint differently — that is what lets a
	// stale redelivery (same hash) stand out from a genuinely new instruction.
	if a == obs.Hash("do the other thing") {
		t.Fatal("Hash collided on distinct input")
	}
}

func TestSummary(t *testing.T) {
	t.Parallel()
	if got := obs.Summary("short", 100); got != "short" {
		t.Fatalf("Summary of a short string = %q, want it unchanged", got)
	}
	long := strings.Repeat("x", 40) + strings.Repeat("y", 40)
	got := obs.Summary(long, 20)
	if !strings.Contains(got, "…[truncated]…") {
		t.Fatalf("Summary of a long string = %q, want an elision marker", got)
	}
	if len(got) >= len(long) {
		t.Fatalf("Summary did not shrink: len %d >= %d", len(got), len(long))
	}
	if !strings.HasPrefix(got, "xxxxxxxxxx") || !strings.HasSuffix(got, "yyyyyyyyyy") {
		t.Fatalf("Summary %q dropped the head or tail", got)
	}
}

func TestHandlerInjectsTurnID(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(obs.Handler(slog.NewJSONHandler(&buf, nil)))

	log.InfoContext(obs.WithTurn(context.Background(), "evt-7"), "brain.tool", "tool", "say")
	if turn := fieldOf(t, buf.Bytes(), obs.TurnKey); turn != "evt-7" {
		t.Fatalf("record turn_id = %q, want evt-7", turn)
	}

	buf.Reset()
	log.InfoContext(context.Background(), "brain.tool", "tool", "say")
	if turn := fieldOf(t, buf.Bytes(), obs.TurnKey); turn != "" {
		t.Fatalf("record without a turn context carried turn_id = %q, want none", turn)
	}
}

// fieldOf decodes the single JSON-lines record in b and returns the string
// value of key, or "" when absent.
func fieldOf(t *testing.T, b []byte, key string) string {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(b), &rec); err != nil {
		t.Fatalf("decode log record %q: %v", b, err)
	}
	s, ok := rec[key].(string)
	if !ok {
		return ""
	}
	return s
}
