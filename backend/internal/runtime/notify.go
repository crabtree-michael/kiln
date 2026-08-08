package runtime

import (
	"context"
	"fmt"
	"log/slog"
)

// Owner resolves a project to its owning user id (11 §3) — the notifier
// path's tenant→recipient hop: push subscriptions hang off a user, not a
// project. The runtime resolves the owner before every Notifier.Send and
// refuses to send for an unowned project (a misdirected push is worse than a
// dropped one); the concrete notifier in cmd/kiln wiring may re-resolve to
// pick the actual subscriptions. nil when identity is unconfigured
// (single-user boot) — then the check is skipped and the notifier targets the
// one local user.
//
// Mode returns the owning user's notification frequency (02 §10 — one of the
// mode* values), which gates whether a given push reaches the user: "blocked"
// delivers only the blocked milestone, "default" all three milestones, "all"
// every feed update. It resolves the owner internally, so a resolution failure
// surfaces the same retryable error Owner does.
type Owner interface {
	Owner(ctx context.Context, projectID string) (string, error)
	Mode(ctx context.Context, projectID string) (string, error)
}

// Notifier executes notify.send entries (02 §10) for one project's owner
// (11 §3 — see Owner). A rare duplicate notification is accepted as benign
// (04 §3).
type Notifier interface {
	Send(ctx context.Context, projectID string, payload []byte) error
}

// Notification frequency modes (02 §10), mirroring push.Mode* by value — this
// module never imports internal/push. The user's mode gates which pushes reach
// them (see notifyModeAllows). modeDefault is the recommended setting and the
// fallback when no owner/mode can be resolved (fail-safe toward the milestones,
// never toward silence).
const (
	modeDefault = "default"
	modeBlocked = "blocked"
	modeAll     = "all"
)

// Notification kinds (02 §10) carried on a push and matched against the mode.
// The three milestones mirror board.NotifyKind* by value; notifyKindUpdate tags
// a feed-update push (proposal/queued/… — every non-milestone activity), which
// only "all" mode delivers.
const (
	notifyKindBlocked = "blocked"
	notifyKindStarted = "started"
	notifyKindDone    = "done"
	notifyKindUpdate  = "update"
)

// notifyModeAllows decides whether a push of the given kind reaches a user on
// the given mode (02 §10). "all" delivers everything; "blocked" only the blocked
// milestone; "default" (and any unrecognized mode, fail-safe) the three genuine
// milestones — blocked, started, done — but not the broader feed-update chatter.
func notifyModeAllows(mode, kind string) bool {
	switch mode {
	case modeAll:
		return true
	case modeBlocked:
		return kind == notifyKindBlocked
	default: // modeDefault, plus any unknown value (fail-safe to the milestones)
		return kind == notifyKindBlocked || kind == notifyKindStarted || kind == notifyKindDone
	}
}

// Notify is the tenant-scoped push choke point: every Web Push the runtime
// emits passes through Send, which applies the owner's mode gate (02 §10) and
// then resolves the owning user before handing the payload to the Notifier
// (11 §3). Both callers — the notify.send outbox topic (a board-emitted
// milestone) and the feed-update push built from feed.updated — go through this
// one type, so the tenant boundary and the mode gate are each enforced in
// exactly one place rather than in two responsibilities that happen to share a
// private method.
//
// The mode gate is deliberately unbypassable from outside: Send is the only
// exported entry point, so no future caller can reach the Notifier without it.
type Notify struct {
	notifier Notifier
	owner    Owner
}

// NewNotify wires the push choke point over its two ports. owner may be nil
// (single-user boot, identity off): the tenant check is then skipped and the
// mode falls back to modeDefault.
func NewNotify(notifier Notifier, owner Owner) *Notify {
	return &Notify{notifier: notifier, owner: owner}
}

// Send is the mode gate in front of the notifier (02 §10): resolve the owning
// user's notification mode, drop the push when the mode does not admit this
// kind, otherwise send it to the project's owner. A mode-resolution failure is
// returned (retry/dead-letter, like any owner-resolution failure) — only an
// admitted-but-undeliverable push is a real send error; a suppressed one is a
// successful no-op.
func (n *Notify) Send(ctx context.Context, projectID, kind string, payload []byte) error {
	mode, err := n.mode(ctx, projectID)
	if err != nil {
		return err
	}
	if !notifyModeAllows(mode, kind) {
		slog.InfoContext(ctx, "runtime.notify.suppressed", "project_id", projectID, "kind", kind, "mode", mode)
		return nil
	}
	return n.sendToOwner(ctx, projectID, payload)
}

// mode resolves the owning user's notification frequency (02 §10). With no
// Owner wired (single-user boot, identity off) there is no per-user mode to read,
// so it falls back to modeDefault — the recommended milestone set, never silence.
func (n *Notify) mode(ctx context.Context, projectID string) (string, error) {
	if n.owner == nil {
		return modeDefault, nil
	}
	mode, err := n.owner.Mode(ctx, projectID)
	if err != nil {
		return "", fmt.Errorf("resolve owner mode: %w", err)
	}
	return mode, nil
}

// sendToOwner is the single call site of Notifier.Send (11 §3): resolve the
// project's owning user first, then send. The owner resolution lives here — not
// in the concrete notifier — because this module is where the tenant boundary is
// enforced: an unowned/unresolvable project must not emit a push at all, and
// returning the error lets the notify.send retry/dead-letter machinery treat a
// transient resolution failure like any delivery failure. The resolved user id is
// a guard + audit anchor; the concrete notifier re-resolves the actual
// subscription targets in wiring. With no Owner wired (single-user boot,
// identity off) the check is skipped.
func (n *Notify) sendToOwner(ctx context.Context, projectID string, payload []byte) error {
	if n.owner != nil {
		userID, err := n.owner.Owner(ctx, projectID)
		if err != nil {
			return fmt.Errorf("resolve project owner: %w", err)
		}
		slog.InfoContext(ctx, "runtime.notify.send", "project_id", projectID, "user_id", userID)
	}
	if err := n.notifier.Send(ctx, projectID, payload); err != nil {
		return fmt.Errorf("notifier send: %w", err)
	}
	return nil
}
