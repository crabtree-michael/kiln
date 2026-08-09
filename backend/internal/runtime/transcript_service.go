package runtime

import (
	"context"
	"fmt"
)

// Nudger wakes the events worker after an ingest (04 §5). The worker is built
// and owned by whoever drains the queue (today Service, later Dispatcher), so
// the transcript reaches it through this one-method hook rather than holding it
// — the ingest→nudge edge is the only thing the conversation surface needs from
// the queue core, and expressing it as an interface keeps the two constructible
// in either order. Nil until wiring calls SetNudger, exactly like the pre-split
// nil-check on the worker itself: an un-nudged ingest still gets picked up by
// the worker's poll fallback, so a missing nudge costs latency, not the event.
type Nudger interface{ NudgeEvents() }

// Transcript is the conversation surface (07 §3): the persisted user↔Kiln
// message log and the live say fan-out over it. Three methods — PostMessage
// (ingest a user turn), Say (emit a Kiln turn), Recent (read the tail) — over
// two ports and the Nudger hook.
//
// The discipline that runs through it is durability-before-visibility: every
// method persists through MessageStore first and only then touches anything
// ephemeral. PostMessage's append+enqueue is one transaction (the transcript
// and the event queue cannot disagree, 07 §3) and nudges only after it commits;
// Say pushes the SSE event only once the kiln row is durable, so a crash
// between the two costs a live push, not history. Nothing here is best-effort
// in the FanOut sense: a failed persist is returned, never logged and dropped.
//
// Backs api's MessagePoster + MessagesReader, and the brain's Say +
// ConversationReader (the latter adapted at the composition root — brain.Message
// is a distinct type, 06 §3.2).
type Transcript struct {
	messages MessageStore
	sayer    SayPusher

	// nudge wakes the events worker after PostMessage's ingest. Set by wiring
	// after the queue core exists (SetNudger); nil-safe until then.
	nudge Nudger
}

// NewTranscript wires the conversation surface over its two ports. The Nudger
// is injected separately (SetNudger) because the value that satisfies it owns
// the workers this ingest wakes, and it is built after this one.
func NewTranscript(messages MessageStore, sayer SayPusher) *Transcript {
	return &Transcript{messages: messages, sayer: sayer}
}

// SetNudger closes the ingest→nudge edge once the queue core is built. Called
// exactly once at wiring time, before any worker is running, so no lock is
// needed. Leaving it unset is safe (see Nudger) — ingest then relies on the
// worker's poll fallback.
func (t *Transcript) SetNudger(n Nudger) { t.nudge = n }

// PostMessage is the runtime's port for POST /api/message (07 §3–§4, api's
// MessagePoster): append the project's user transcript row and enqueue the
// human.message event {text} in one transaction (MessageStore's job), then
// nudge the events worker. Returns both ids for the 202 response
// ({event_id, message_id}); a failed append surfaces as an error with no
// invented, partial ids (07 §3 — the transcript and the queue cannot disagree).
func (t *Transcript) PostMessage(ctx context.Context, projectID, text string) (int64, int64, error) {
	messageID, eventID, err := t.messages.AppendUserMessageAndEnqueueEvent(ctx, projectID, text)
	if err != nil {
		return 0, 0, fmt.Errorf("runtime: post message: %w", err)
	}
	t.nudgeEvents()
	return messageID, eventID, nil
}

// Say is the runtime's Say port (07 §3, §6; also brain.Say, matched
// structurally with no adapter): append the project's kiln transcript row,
// then push a say SSE event ({message_id, text, at}) to that project via
// SayPusher. Append-then-push — a crash between them costs a live push, not
// history (07 §3), so the push only ever fires once the row is durable. Every
// user-visible reply goes through this, including the dead-letter
// system-error message.
func (t *Transcript) Say(ctx context.Context, projectID, text string) error {
	m, err := t.messages.AppendKilnMessage(ctx, projectID, text)
	if err != nil {
		return fmt.Errorf("runtime: say append: %w", err)
	}
	if err := t.sayer.PushSay(ctx, projectID, m); err != nil {
		return fmt.Errorf("runtime: say push: %w", err)
	}
	return nil
}

// Recent is the runtime's ConversationReader-shaped read (07 §3): the
// project's last n transcript rows, oldest first. Backs GET /api/messages
// (api's MessagesReader) directly, and the brain's ConversationReader port
// through a composition-root adapter (brain.Message is a distinct type —
// 06 §3.2).
func (t *Transcript) Recent(ctx context.Context, projectID string, n int) ([]Message, error) {
	msgs, err := t.messages.Recent(ctx, projectID, n)
	if err != nil {
		return nil, fmt.Errorf("runtime: recent: %w", err)
	}
	return msgs, nil
}

// nudgeEvents wakes the events worker through the hook if one is wired (04 §5).
// No-op before SetNudger runs, so ingestion still works during startup — the
// poll fallback catches the row.
func (t *Transcript) nudgeEvents() {
	if t.nudge != nil {
		t.nudge.NudgeEvents()
	}
}
