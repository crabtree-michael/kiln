// Ticket detail sheet. Opening a board card slides a bottom sheet up into view
// from the bottom edge (a classic mobile sheet) showing the ticket's full
// record — everything the card elides: the complete body, priority, timestamps,
// id, and (when blocked) the full blocked reason. It is read-only inspection
// over the read-only board (D5) as far as the ticket's *state* goes: Accept,
// Delete and Poke all only express intent, which the caller routes through the
// brain. It writes exactly two things directly, both of them the user's own
// input rather than a board transition:
//
//   • the per-ticket sandbox option (`onSetKeepSandbox`) — a setting on the
//     ticket; and
//   • the ticket's own title/body text (`onEditText`) — the edit affordance in
//     the header, which turns the title and body into fields the user types in.
//
// It also carries the two manual sandbox overrides (`onKillSandbox`,
// `onReassignSandbox`), which are direct writes for a third reason again: they
// exist precisely to let the user reach past the orchestrator when a sandbox is
// wedged or its working tree is corrupted, so routing them through the brain
// would reinstate the wait they were built to remove. They act on the sandbox
// behind the ticket's slot rather than on the ticket's place on the board, which
// is why they are not board transitions at all.
//
// The text edit is deliberately NOT routed through the brain, and for a sharper
// reason than the sandbox toggle: describing a wording change out loud and
// letting the brain rewrite the ticket is exactly what drifts from what the user
// meant, so a direct edit exists precisely to put the typed text on the board
// verbatim. It is offered only while the ticket is still in the backlog
// (EDITABLE_STATES) — past that, the text is what the agent was briefed with.
//
// The slide-up entrance + native rubber-band overscroll and drag-to-dismiss come
// from `vaul` (direction="bottom") — the standard React drawer/sheet, adopted
// with explicit user sign-off waiving the former blanket no-library rule
// (07 D4). Vaul owns dismissal entirely: dragging the sheet back down past the
// threshold, clicking the scrim, and pressing Escape all route through
// `onOpenChange(false)` → `onClose`, so this component adds none of that by
// hand — dismiss stays low-friction, never a trap (07 §7–§8).
import { useEffect, useState, type JSX, type ReactNode } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { Drawer } from 'vaul';
import type { Ticket } from '@/components/TicketCard';
import type { components } from '@/schema/generated';
import '@/components/TicketDetail.css';

/** One direct text edit: the fields the user actually changed. Both are
 * optional and only changed ones are sent, so an edit to the title alone can't
 * clobber a body the brain rewrote while the sheet was open. It is the wire
 * request body itself (POST /api/tickets/{id}/text), taken straight from the
 * generated schema so the sheet and the transport can't drift. */
export type TicketTextEdit = components['schemas']['TicketTextRequest'];

export interface TicketDetailProps {
  ticket: Ticket;
  onClose: () => void;
  /** When provided, the detail is a proposal reached via click-through and shows
   * an Accept action (08 §5) — accept after reading the full ticket. Omitted →
   * the overlay stays strictly read-only (D5).
   * Accept only appears while the ticket is still shaping: accepting is what
   * moves a shaped proposal into the pull, so every later state (ready, working,
   * blocked, done) has already passed that point and shows no button regardless. */
  onAccept?: (ticketId: string) => void;
  /** When provided, the sheet shows a Delete action in the bottom-left for the
   * states in DELETABLE_STATES — a shaping proposal the user wants to discard, or
   * a blocked ticket stuck in development (a duplicate the pull already picked up).
   * The other states show no button: working has a live agent mid-turn, and
   * ready/done are out of this first pass. The caller routes the deletion through
   * the brain (D5), which archives the ticket via delete_ticket — and for a
   * blocked ticket the board also releases the worker it held. Omitted → no Delete
   * affordance (read-only inspection). */
  onDelete?: ((ticketId: string) => void) | undefined;
  /** When provided on a *working* or *blocked* ticket, a "Poke to continue" button
   * appears — a manual nudge for a stalled agent, mirroring the steward's own
   * mechanical poke. Tapping it expresses the user's "continue" intent for this
   * ticket; the caller routes that through the brain (which decides to
   * send_to_agent(id, "continue")) — the client never commands an agent directly
   * (D5). On a *working* ticket the nudge only makes sense once the agent has gone
   * quiet, so it's further gated on `agentIdle` (below); on a *blocked* ticket the
   * work is stalled by definition, so Poke shows whenever wired. Omitted → no Poke
   * affordance (read-only inspection). */
  onPoke?: ((ticketId: string) => void) | undefined;
  /** When provided, the sheet shows the per-ticket **sandbox option** — a switch
   * reading the ticket's own `keep_sandbox`. Saving a ticket's sandbox stops the
   * board releasing its worker when the ticket leaves Developing, so the workspace
   * survives and an agent can keep working in that same sandbox across turns.
   * Unlike Accept/Delete/Poke this is a setting rather than an intent, so the
   * caller writes it directly (POST /api/tickets/{id}/sandbox) rather than routing
   * it through the brain, and the sheet stays open after the toggle — the user may
   * well flip it and keep reading. Offered on every lifecycle state: the choice
   * matters before the sandbox exists (a proposal the user already knows they want
   * to keep working in) just as much as while it's running. Omitted → no switch,
   * so a read-only sheet is unchanged. */
  onSetKeepSandbox?: ((ticketId: string, keep: boolean) => void) | undefined;
  /** When provided on a ticket that has a sandbox (working|blocked), the sheet
   * shows **Kill sandbox** — destroy the workspace this ticket's agent is in so
   * its slot comes back with a fresh one. This is the manual override for a
   * wedged or corrupted sandbox: the thing that previously meant waiting for the
   * orchestrator to notice and clean up. Like the sandbox switch (and unlike
   * Accept/Delete/Poke) the caller writes it directly rather than routing it
   * through the brain — an override that waits on an LLM turn is not an override.
   * Destructive and irreversible, so it is gated behind a confirm tap. Omitted →
   * no button. */
  onKillSandbox?: ((ticketId: string) => void) | undefined;
  /** When provided on a ticket that has a sandbox (working|blocked), the sheet
   * shows **Move to a new sandbox** — the recovery counterpart to Kill: the
   * ticket is bound to a different free sandbox and the agent there is briefed
   * with the ticket's work order from scratch, while the old workspace is thrown
   * away. Use it when the workspace is beyond saving and the work should simply
   * start again somewhere clean. Also a direct write, also confirm-gated (it
   * discards whatever the current agent had done). Omitted → no button. */
  onReassignSandbox?: ((ticketId: string) => void) | undefined;
  /** Whether there is a free sandbox to move this ticket to, from the board
   * snapshot's `worker_free`. False disables the Move button and says why, so the
   * user isn't offered an action the server would only refuse with a 409 — with
   * every slot busy there is nowhere to go and Kill is the option left. Defaults
   * true (unknown capacity → offer it and let the server be the judge). */
  canReassign?: boolean;
  /** The live session status of this ticket's sandbox, from the board snapshot's
   * `agents` join — the same lookup that feeds `agentIdle`, shown verbatim beside
   * the controls. It is what makes Kill a considered action rather than a blind
   * one: an `errored` or `stopped` sandbox is the case the control exists for,
   * and a `building` one warns that a turn is being cut short. Omitted → the
   * status line says the sandbox isn't reporting, which is itself a signal. */
  sandboxStatus?: string | undefined;
  /** When provided, the sheet shows an **edit** affordance beside the title (a
   * pencil) for a ticket still in EDITABLE_STATES, turning the title and body
   * into a text field and a textarea the user types in directly. Saving calls
   * this with only the fields that actually changed. Unlike Accept/Delete/Poke
   * it carries no intent for the brain to interpret: the caller writes the text
   * straight to the board (POST /api/tickets/{id}/text), because an LLM pass
   * between the user and their own words is the drift this affordance exists to
   * remove. Like the sandbox switch the sheet stays open afterwards — the user
   * saves and reads the result. Omitted → no pencil, so a read-only sheet is
   * unchanged. */
  onEditText?: ((ticketId: string, patch: TicketTextEdit) => void) | undefined;
  /** The live session status of this ticket's bound agent, from the board
   * snapshot's `agents` join (`AgentStatus.status === 'idle'`). A *working* ticket
   * only offers Poke when this is true — the agent is alive but between turns and
   * waiting for input. While it's mid-turn (`building`, progress streaming) Poke is
   * hidden, so the user isn't invited to nudge an agent that's already moving.
   * Defaults false (unknown / no bound agent → treat as not-idle, so no Poke). */
  agentIdle?: boolean;
  /** The mic control rendered at the footer's bottom-left on *every* ticket state
   * — the unified communication surface (08 §5). It replaces the old blocked-only
   * "Talk to unblock" button so all ticket types share one interface: the user can
   * start talking to the brain directly from any ticket without leaving the sheet.
   * It is the same mic-orb button as the main screen's dock (`MicButton`); tapping
   * it starts a voice recording session and the transcript lands in the sheet's own
   * dock (see `transcript`). Passed in rather than rendered here so this component
   * stays free of the voice store: it is a live `useVoice()` consumer and only the
   * primary screen (under a `VoiceProvider`) wires it — a sheet opened without one
   * keeps the mic omitted (read-only inspection). */
  voiceControl?: ReactNode;
  /** The live voice transcript, rendered in the sheet's dock directly above the
   * action buttons — the on-screen feedback for `voiceControl`, so it rides the
   * same gate (shown whenever the mic is wired, i.e. any state on the primary
   * screen). It shows the words as the user speaks to the brain, so they never
   * have to leave the sheet to watch the transcript land. Like `voiceControl` it is
   * passed in rather than rendered here (it is a live `useVoice()` consumer) so this
   * component stays free of the voice store; only the primary screen (under a
   * `VoiceProvider`) wires it. The node self-gates — it renders nothing unless there
   * is transcript text on screen — so the dock only grows while the user is actually
   * speaking. Omitted on any sheet opened without voice. */
  transcript?: ReactNode;
  /** Which surface's skin to wear. The sheet portals to `document.body` (so its
   * fixed positioning escapes any transformed/clipping ancestor), which lifts it
   * out of the `[data-role='primary-screen']` subtree the skin CSS used to key
   * off — so the surface is now carried explicitly on the panel as
   * `data-surface`. Defaults to the base/denser register; the primary screen
   * passes `'primary'` for the app's first-class card skin (08 §5). */
  surface?: 'debug' | 'primary';
}

/** The header status badge — a dot + word pinned to the header's top-right that
 * names the ticket's lifecycle state at a glance, so it's always obvious what's
 * happening with the work (07 §7). Only the three states carrying a clear signal
 * get one, each in its own semantic colour:
 *   • working → "In progress" (ember, pulsing — the eye-drawing live state)
 *   • blocked → "Blocked" (fire — the loudest surface; the full reason renders
 *               below the header)
 *   • done    → "Done" (glaze/all-clear)
 * shaping/ready are the neutral "awaiting action" states and wear no badge —
 * shaping instead offers the Accept button. */
const STATUS_LABELS: Partial<Record<Ticket['state'], string>> = {
  working: 'In progress',
  blocked: 'Blocked',
  done: 'Done',
};

/** The lifecycle states whose detail sheet offers a Delete button, when the
 * caller wires `onDelete`. Shaping (discard a proposal) and blocked (delete a
 * ticket stuck in development — a duplicate the pull already picked up, whose
 * worker the board releases on archive) are deletable directly; every other
 * state is not (working has a live agent mid-turn; done/ready are left out of
 * this first pass — 2026-07-11-delete-blocked-ticket-design.md D3). This set is
 * the single seam for widening the affordance to another state later. */
const DELETABLE_STATES = new Set<Ticket['state']>(['shaping', 'blocked']);

/** Confirm copy for deleting a *blocked* ticket. Unlike discarding a shaping
 * proposal (cheap, re-proposable, so it deletes immediately), deleting a blocked
 * ticket tears down its worker and discards the in-progress development work, and
 * there is no un-archive in the product — so it is gated behind a confirm that
 * names the consequence (D4). */
const DELETE_BLOCKED_CONFIRM =
  "Delete this blocked ticket? Its in-progress work will be discarded and can't be recovered here.";

/** How long the sandbox switch holds the user's choice before deferring to the
 * board snapshot again. The write is fire-and-forget (the new value arrives on
 * the next `board.updated`), so the switch shows the choice immediately and this
 * time-box is what self-heals it if the write never lands — the same shape the
 * feed store's optimistic card hides use. */
const SANDBOX_OPTIMISTIC_MS = 5000;

/** The lifecycle states whose text the user may edit directly, when the caller
 * wires `onEditText`. It mirrors the board's own `shape_ticket` precondition
 * (shaping or ready) rather than restating a client-side opinion: shaping is
 * where wording gets refined before work starts, ready is still queued and not
 * yet briefed to anyone. Once a ticket is working/blocked/done its text is what
 * an agent was actually briefed with, so the board refuses the write (409) and
 * the sheet offers no pencil. This set is the single seam for widening the
 * affordance later — widen the board's precondition with it. */
const EDITABLE_STATES = new Set<Ticket['state']>(['shaping', 'ready']);

/** The lifecycle states that actually have a sandbox behind them, and so the
 * only ones the Kill / Move controls appear on. It mirrors the board's own
 * precondition (a worker is bound iff the ticket is working or blocked) rather
 * than restating a client-side opinion — on any other state the server refuses
 * with a 409, so offering the button would only produce an error. */
const SANDBOX_CONTROL_STATES = new Set<Ticket['state']>(['working', 'blocked']);

/** How the sandbox's live session status reads in the sheet's status line. The
 * wire values (05 §2) are neutral machine words; these are the user-facing
 * sentences that say what each one means for the decision in front of them —
 * whether killing this sandbox interrupts something or rescues it. */
const SANDBOX_STATUS_LABELS: Record<string, string> = {
  building: 'working now',
  idle: 'idle',
  stopped: 'stopped',
  errored: 'failing',
  starting: 'starting up',
};

/** How long a saved edit is shown before the sheet defers to the board snapshot
 * again. The write is fire-and-forget (the new text arrives on the next
 * `board.updated`), so the sheet shows what was typed immediately and this
 * time-box is what self-heals it if the write never lands — the same shape the
 * sandbox switch above uses. */
const TEXT_OPTIMISTIC_MS = 5000;

export function TicketDetail({
  ticket,
  onClose,
  onAccept,
  onDelete,
  onPoke,
  onSetKeepSandbox,
  onKillSandbox,
  onReassignSandbox,
  canReassign = true,
  sandboxStatus,
  onEditText,
  agentIdle = false,
  voiceControl,
  transcript,
  surface = 'debug',
}: TicketDetailProps): JSX.Element {
  // Which affordances the sheet's footer carries is decided purely by lifecycle
  // state, so the caller can't wire a nonsensical one:
  //  • every state     → the mic (when wired): the unified communication surface
  //                      (08 §5) — the user can start talking to the brain from any
  //                      ticket. Replaces the old blocked-only "Talk to unblock"
  //                      button so all ticket types share one interface.
  //  • shaping         → Accept (when wired): the proposal click-through (08 §5) —
  //                      accepting is what moves a shaped proposal into the pull,
  //                      so it only makes sense here. Every later state has already
  //                      been accepted, so the button is gone.
  //  • working|blocked → Poke (when wired): a manual nudge to continue for a
  //                      stalled agent, routed through the brain (never a direct
  //                      agent command, D5). On a working ticket it only shows once
  //                      the agent is idle (`agentIdle`) — never mid-turn; on a
  //                      blocked ticket it always shows.
  //  • done            → no action but the mic; the header badge already says "Done".
  // The footer branches below narrow on the callbacks directly (not derived
  // booleans) so TypeScript knows they're defined inside the handler — no
  // optional chain, which the lint gate rejects (mirrors FeedCardItem).
  // The sandbox switch renders the user's choice at once, then hands back to the
  // board snapshot: `pendingKeep` is null whenever the snapshot is authoritative.
  // It clears as soon as the snapshot agrees, and otherwise on the time-box — so a
  // write that never lands can't leave the switch stuck on a lie.
  const [pendingKeep, setPendingKeep] = useState<boolean | null>(null);
  const serverKeep = ticket.keep_sandbox;
  useEffect(() => {
    if (pendingKeep === null) {
      return;
    }
    if (pendingKeep === serverKeep) {
      setPendingKeep(null);
      return;
    }
    const timer = setTimeout(() => {
      setPendingKeep(null);
    }, SANDBOX_OPTIMISTIC_MS);
    return () => {
      clearTimeout(timer);
    };
  }, [pendingKeep, serverKeep]);

  // Which sandbox override is one tap from firing, if any. Both are destructive
  // and neither can be undone — a killed workspace is gone, and a moved ticket
  // starts over from the work order — so each takes two taps: the first arms the
  // button (its label becomes the consequence), the second commits. Arming one
  // disarms the other, and the arming clears the moment the sheet is showing a
  // different ticket, so a stray tap can never land on the wrong sandbox.
  const [armed, setArmed] = useState<'kill' | 'reassign' | null>(null);
  const ticketId = ticket.id;
  useEffect(() => {
    setArmed(null);
  }, [ticketId]);

  // The direct text edit, in two pieces of state:
  //  • `draft` is non-null exactly while the user is editing, and holds what
  //    they have typed so far. Entering edit mode seeds it from what is on
  //    screen; Cancel throws it away.
  //  • `pendingText` is the just-saved text, shown until the board snapshot
  //    catches up. Same self-healing shape as `pendingKeep`: it clears the
  //    moment the snapshot agrees, and otherwise on a time-box, so a write that
  //    never lands can't leave the sheet showing text the board doesn't have.
  const [draft, setDraft] = useState<{ title: string; body: string } | null>(null);
  const [pendingText, setPendingText] = useState<{ title: string; body: string } | null>(null);
  const serverTitle = ticket.title;
  const serverBody = ticket.body;
  useEffect(() => {
    if (pendingText === null) {
      return;
    }
    if (pendingText.title === serverTitle && pendingText.body === serverBody) {
      setPendingText(null);
      return;
    }
    const timer = setTimeout(() => {
      setPendingText(null);
    }, TEXT_OPTIMISTIC_MS);
    return () => {
      clearTimeout(timer);
    };
  }, [pendingText, serverTitle, serverBody]);
  // What the sheet renders: the optimistic text while a save is in flight,
  // otherwise the board's own. Everything downstream (the title, the Markdown
  // body, the edit draft's seed) reads these, never the raw ticket, so the saved
  // wording never flickers back to the pre-edit text for one snapshot.
  const shownTitle = pendingText === null ? serverTitle : pendingText.title;
  const shownBody = pendingText === null ? serverBody : pendingText.body;

  const isShaping = ticket.state === 'shaping';
  const isBlocked = ticket.state === 'blocked';
  const isWorking = ticket.state === 'working';
  const statusLabel = STATUS_LABELS[ticket.state];
  // Whether each footer action can appear: the ticket's lifecycle state plus
  // whether the caller wired the callback. These decide only if the actions row
  // renders at all — each button below re-checks its own callback directly so
  // TypeScript narrows it to defined (a derived boolean wouldn't narrow, and the
  // lint gate rejects the optional chain the alternative would need).
  const canPoke = onPoke !== undefined && (isBlocked || (isWorking && agentIdle));
  const canAccept = isShaping && onAccept !== undefined;
  // The bottom-left lead cluster holds the sheet's secondary affordances — the
  // voice mic and the Delete button — wired only on the primary screen (a
  // read-only sheet leaves both undefined). They sit left of the trailing
  // Accept/Poke — the bottom-left pair 08 §5 calls for. The mic now shows on every
  // ticket state (the unified communication surface — start talking from any
  // ticket), so it is gated only on being wired; Delete shows in any
  // DELETABLE_STATES state (shaping or blocked).
  const showVoice = voiceControl !== undefined;
  const canDelete = DELETABLE_STATES.has(ticket.state) && onDelete !== undefined;
  // Whether the sheet is in edit mode. The pencil that enters it is gated
  // inline on EDITABLE_STATES + onEditText (a backlog ticket whose text the
  // board will still accept, with the write wired) rather than on a derived
  // boolean, so TypeScript narrows the callback inside its handler.
  const editing = draft !== null;
  // A ticket must keep a name: the title is its whole identity on the board and
  // in the feed, and the server rejects a blank one, so Save is disabled rather
  // than letting the user submit into a 400. An empty *body* is a legal edit.
  const canSave = draft !== null && draft.title.trim() !== '';
  // The dock is the sheet's bottom region — the unified home for the action
  // controls AND the live voice transcript (08 §5), the mirror of the primary
  // screen's own dock. It renders whenever any footer affordance does; the
  // transcript, when present, stacks above the controls inside it and grows the
  // dock upward as words stream in. The transcript rides the same gate as the mic
  // (`showVoice`) — it is that mic's on-screen feedback. While editing it always
  // renders, because Cancel/Save live there and replace the state actions
  // wholesale: mid-edit is no moment to be offered Accept or a mic.
  const showDock = editing || showVoice || canPoke || canDelete || canAccept;
  // The manual sandbox overrides. They render only where a sandbox exists to act
  // on (working|blocked) and only when the caller wired at least one of them —
  // the same shape as every other affordance here, so a read-only sheet is
  // unchanged. Each button still re-checks its own callback inline so TypeScript
  // narrows it inside the handler.
  const showSandboxControls =
    SANDBOX_CONTROL_STATES.has(ticket.state) &&
    (onKillSandbox !== undefined || onReassignSandbox !== undefined);
  // The status line reads the sandbox's own session state, not the ticket's board
  // column. An unknown status is reported as such rather than guessed at: "not
  // reporting" is itself the signal that something is wrong with the sandbox,
  // which is exactly when these controls matter.
  const sandboxStatusLabel =
    sandboxStatus === undefined
      ? 'not reporting'
      : (SANDBOX_STATUS_LABELS[sandboxStatus] ?? sandboxStatus);
  // What the two buttons mean, in one sentence — and, when there is no free
  // sandbox to move to, why the Move button is greyed out, so a disabled control
  // never reads as a bug.
  const sandboxControlsHint = canReassign
    ? 'Killing the sandbox throws its workspace away and brings a fresh one up in its place, ' +
      'leaving this ticket where it is. Moving it also starts the work over on a different sandbox.'
    : 'Killing the sandbox throws its workspace away and brings a fresh one up in its place. ' +
      'Every other sandbox is busy right now, so there is none free to move this ticket to.';

  /** Commit the draft. Only the fields that actually changed are sent, so an
   * edit to one can't overwrite the other with the text the sheet happened to
   * open with (the brain may have rewritten it meanwhile). An unchanged draft
   * sends nothing at all — closing the editor is the whole effect, rather than a
   * write that would still fan a board.updated out to every open client. */
  function saveDraft(): void {
    if (draft === null || onEditText === undefined || !canSave) {
      return;
    }
    const title = draft.title.trim();
    const { body } = draft;
    const patch: TicketTextEdit = {};
    if (title !== shownTitle) {
      patch.title = title;
    }
    if (body !== shownBody) {
      patch.body = body;
    }
    if (patch.title !== undefined || patch.body !== undefined) {
      setPendingText({ title, body });
      onEditText(ticket.id, patch);
    }
    setDraft(null);
  }
  return (
    // `open` is fixed true: this component only mounts while a ticket is
    // selected, so Vaul's own open/closed state just mirrors that. Every dismiss
    // path (drag past threshold, scrim click, Escape) fires onOpenChange(false),
    // which we forward to onClose — the caller then unmounts us.
    <Drawer.Root
      // Bottom-anchored: slides up into view from the bottom edge (07 §7 — a
      // classic mobile sheet).
      direction="bottom"
      open
      onOpenChange={(next) => {
        if (!next) {
          onClose();
        }
      }}
    >
      <Drawer.Portal>
        <Drawer.Overlay data-role="ticket-detail-backdrop" />
        <Drawer.Content
          // Radix (Vaul's base) owns role="dialog"/aria-modal and writes its own
          // data-state=open|closed for the slide animation — so the ticket's
          // lifecycle state rides on data-ticket-state to avoid clobbering it,
          // and the surface skin on data-surface (see the prop's doc). The dialog
          // is named by its <Drawer.Title> (the visible ticket title) via the
          // aria-labelledby Radix wires up, so no aria-label is needed here.
          // No description element; tell Radix so on purpose rather than warn.
          aria-describedby={undefined}
          data-role="ticket-detail"
          data-ticket-state={ticket.state}
          data-surface={surface}
        >
          {/* A bottom sheet's drag affordance sits on its upper edge — the
              grabber is the first child, above the header. */}
          <Drawer.Handle data-role="ticket-detail-grabber" />

          <header data-role="ticket-detail-header">
            {/* Title and its lifecycle badge stack in a left-aligned column so the
                title gets the full header width instead of ceding room to a badge
                on its right. */}
            <div data-role="ticket-detail-heading">
              {/* The title is always rendered, even mid-edit: Radix names the
                  dialog by this element (aria-labelledby), so replacing it with
                  an input would leave the sheet nameless. While editing it is
                  visually hidden instead (data-editing, clipped in CSS) and the
                  field below takes its place on screen — the accessible name
                  stays the ticket's saved title throughout. */}
              <Drawer.Title
                data-role="ticket-detail-title"
                data-editing={editing ? 'true' : undefined}
              >
                {shownTitle}
              </Drawer.Title>
              {draft !== null && (
                <input
                  type="text"
                  data-role="detail-edit-title"
                  aria-label="Title"
                  value={draft.title}
                  // Vaul reads pointer drags on the sheet as dismiss gestures;
                  // opt the fields out so selecting text inside one doesn't drag
                  // the whole sheet away mid-edit.
                  data-vaul-no-drag
                  autoFocus
                  onChange={(event) => {
                    setDraft({ title: event.target.value, body: draft.body });
                  }}
                />
              )}
              {/* The lifecycle badge: a dot + word directly under the title that
                  names the ticket's state at a glance (In progress / Blocked /
                  Done), each in its own colour. Only the states that carry a
                  signal show one; shaping/ready wear none. Keyed on data-state
                  (not Radix's own data-state, which lives on the panel) for its
                  per-state colour. */}
              {statusLabel !== undefined && (
                <span data-role="ticket-detail-status" data-state={ticket.state}>
                  <span data-role="ticket-detail-status-dot" aria-hidden="true" />
                  {statusLabel}
                </span>
              )}
            </div>
            {/* The edit affordance, beside the title: a pencil that turns the
                title and body into fields. Icon-only like Delete/Poke, so its
                accessible name comes from aria-label; the glyph is aria-hidden.
                Hidden once editing starts — the sheet is already in that mode,
                and Cancel/Save in the dock are the way out. Narrows on
                onEditText directly (not the derived canEdit) so TypeScript knows
                it's defined in the handler, mirroring the dock's buttons. */}
            {EDITABLE_STATES.has(ticket.state) && onEditText !== undefined && !editing && (
              <button
                type="button"
                data-role="ticket-detail-edit"
                aria-label="Edit"
                onClick={() => {
                  setDraft({ title: shownTitle, body: shownBody });
                }}
              >
                <svg
                  viewBox="0 0 24 24"
                  width="16"
                  height="16"
                  aria-hidden="true"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                >
                  <path d="M4 20h4l10-10a2.1 2.1 0 0 0-3-3L5 17v3" />
                  <path d="M14.5 6.5l3 3" />
                </svg>
              </button>
            )}
            <button
              type="button"
              data-role="ticket-detail-close"
              aria-label="Close"
              onClick={onClose}
            >
              ×
            </button>
          </header>

          {/* The scroll region: the block message and the Markdown body live
              together inside the one overflowing area, so a long block message
              scrolls with the body instead of being clipped by the panel's
              overflow: hidden. Both sit under the pinned header/meta.

              While editing, the whole region becomes the body textarea: the
              rendered Markdown is what the user is replacing, and the sandbox
              switch is a different kind of decision that has no place in the
              middle of typing. The blocked reason never appears here either —
              a blocked ticket is not editable in the first place. */}
          <div data-role="ticket-detail-body" data-editing={editing ? 'true' : undefined}>
            {draft !== null ? (
              <textarea
                data-role="detail-edit-body"
                aria-label="Description"
                value={draft.body}
                data-vaul-no-drag
                onChange={(event) => {
                  setDraft({ title: draft.title, body: event.target.value });
                }}
              />
            ) : (
              <>
                {ticket.state === 'blocked' && ticket.blocked_reason != null && (
                  <p data-role="detail-blocked-reason">{ticket.blocked_reason}</p>
                )}
                <ReactMarkdown remarkPlugins={[remarkGfm]}>{shownBody}</ReactMarkdown>
                {/* The per-ticket sandbox option, at the foot of the scroll region:
                it is a setting the user reads and considers, not one of the
                dock's one-tap actions, so it sits with the ticket's own record
                rather than competing with Accept/Poke/Delete for the thumb. The
                label wraps the checkbox, so the whole row is the hit target and
                the accessible name needs no aria plumbing. */}
                {onSetKeepSandbox !== undefined && (
                  <div data-role="detail-sandbox">
                    <label data-role="detail-sandbox-switch">
                      <input
                        type="checkbox"
                        data-role="detail-keep-sandbox"
                        checked={pendingKeep ?? serverKeep}
                        onChange={(event) => {
                          setPendingKeep(event.target.checked);
                          onSetKeepSandbox(ticket.id, event.target.checked);
                        }}
                      />
                      Save this ticket&rsquo;s sandbox
                    </label>
                    <p data-role="detail-sandbox-hint">
                      A saved sandbox isn&rsquo;t torn down when the ticket leaves development, so
                      an agent can keep working in the same workspace across turns.
                    </p>
                  </div>
                )}
                {/* The manual sandbox overrides, directly under the save option
                they argue with: one row says "keep this workspace", the next
                says "throw it away", and reading them together is what makes the
                choice obvious. Only on a ticket that actually has a sandbox. */}
                {showSandboxControls && (
                  <div data-role="detail-sandbox-controls">
                    <p data-role="detail-sandbox-status">
                      This ticket&rsquo;s sandbox is {sandboxStatusLabel}.
                    </p>
                    <div data-role="detail-sandbox-buttons">
                      {onKillSandbox !== undefined && (
                        <button
                          type="button"
                          data-role="detail-kill-sandbox"
                          data-armed={armed === 'kill' ? 'true' : undefined}
                          onClick={() => {
                            if (armed !== 'kill') {
                              setArmed('kill');
                              return;
                            }
                            setArmed(null);
                            onKillSandbox(ticket.id);
                          }}
                        >
                          {armed === 'kill' ? 'Destroy it — tap to confirm' : 'Kill sandbox'}
                        </button>
                      )}
                      {onReassignSandbox !== undefined && (
                        <button
                          type="button"
                          data-role="detail-reassign-sandbox"
                          data-armed={armed === 'reassign' ? 'true' : undefined}
                          disabled={!canReassign}
                          onClick={() => {
                            if (armed !== 'reassign') {
                              setArmed('reassign');
                              return;
                            }
                            setArmed(null);
                            onReassignSandbox(ticket.id);
                          }}
                        >
                          {armed === 'reassign'
                            ? 'Start over there — tap to confirm'
                            : 'Move to a new sandbox'}
                        </button>
                      )}
                    </div>
                    <p data-role="detail-sandbox-controls-hint">{sandboxControlsHint}</p>
                  </div>
                )}
              </>
            )}
          </div>

          {/* Footer actions. Which affordances appear is decided purely by the
              ticket's lifecycle state and which callbacks the caller wired, so a
              nonsensical action can't be shown:
               • Mic    → every state: the unified communication surface (08 §5) —
                          start talking to the brain from any ticket. Lives in the
                          bottom-left lead cluster; replaces the old blocked-only
                          "Talk to unblock" button.
               • Poke   → working|blocked: nudge a stalled agent to continue. Only
                          expresses intent — the caller routes it through the brain
                          (D5), never a direct agent command.
               • Delete → shaping|blocked: discard a ticket that's no longer wanted,
                          routed through the brain (delete_ticket, D5). A
                          destructive secondary sitting left of Accept.
               • Accept → the proposal click-through (08 §5), shaping-only (every
                          later state has already been accepted).
              The lead cluster (mic + Delete) and Poke sit first (left); the state's
              primary action (Accept) stays rightmost, where flex-end makes it the
              most prominent. The quiet affordances here read as icons only — the mic
              glyph, the 👉 for Poke, the trash for Delete — with no text label around
              them; Accept alone carries a word, so the one headline decision is the
              only thing spelled out. Each button narrows on its callback directly
              inside the guard so TypeScript knows it's defined in the handler — no
              optional chain (the lint gate). */}
          {showDock && (
            <div data-role="ticket-detail-dock">
              {/* The live voice transcript, above the controls (08 §5): the sheet's
                  dock, like the primary screen's, carries both the controls and the
                  transcript, growing upward as the words stream in. Rides the same
                  gate as the mic (`showVoice`, wired on every state); the node
                  self-gates further on there being transcript text, so it takes no
                  room until the user speaks. Suppressed while editing: the mic is
                  gone in that mode, so its feedback has nothing to show. */}
              {!editing && showVoice && transcript}
              {/* Editing replaces the state actions wholesale with Cancel/Save.
                  Mid-edit is no moment to be offered Accept, Delete or a mic, and
                  the two ways out of the mode are the only controls that matter —
                  so the row below is skipped entirely rather than grown. */}
              {editing ? (
                <div data-role="ticket-detail-actions">
                  <button
                    type="button"
                    data-role="detail-edit-cancel"
                    onClick={() => {
                      setDraft(null);
                    }}
                  >
                    Cancel
                  </button>
                  <button
                    type="button"
                    data-role="detail-edit-save"
                    // A blank title is refused by the server, so the button is
                    // dead rather than inviting the user into a 400.
                    disabled={!canSave}
                    onClick={saveDraft}
                  >
                    Save
                  </button>
                </div>
              ) : (
                <div data-role="ticket-detail-actions">
                  {/* Bottom-left cluster: the mic. `margin-right: auto` on it pushes
                  the trailing state actions (Delete/Accept/Poke) to the right;
                  absent (a sheet without voice) the row is byte-identical to the
                  old flex-end footer. */}
                  {showVoice && <div data-role="ticket-detail-lead-actions">{voiceControl}</div>}
                  {(isBlocked || (isWorking && agentIdle)) && onPoke !== undefined && (
                    <button
                      type="button"
                      data-role="detail-poke"
                      aria-label="Poke"
                      onClick={() => {
                        onPoke(ticket.id);
                      }}
                    >
                      {/* Icon-only, matching Delete: the 👉 is the whole visible
                      signal for a poke (mirroring the feed's poke card, 08 §3),
                      with no text label around it. The glyph is aria-hidden and the
                      button's accessible name comes from aria-label="Poke". */}
                      <span data-role="detail-poke-emoji" aria-hidden="true">
                        👉
                      </span>
                    </button>
                  )}
                  {/* Delete shows for a DELETABLE_STATES ticket with onDelete wired,
                  as an icon-only circular button to the left of Accept. Inline the
                  state + callback check (not the derived canDelete) so TypeScript
                  narrows onDelete to defined inside onClick — mirroring the
                  Poke/Accept buttons. The trash glyph is aria-hidden, so the
                  button's accessible name comes from aria-label="Delete". */}
                  {DELETABLE_STATES.has(ticket.state) && onDelete !== undefined && (
                    <button
                      type="button"
                      data-role="detail-delete"
                      aria-label="Delete"
                      onClick={() => {
                        // A blocked delete discards in-progress work and releases a
                        // worker, with no un-archive — so confirm it (D4). A shaping
                        // proposal is cheap and re-proposable: delete it immediately,
                        // no confirm.
                        if (ticket.state === 'blocked' && !window.confirm(DELETE_BLOCKED_CONFIRM)) {
                          return;
                        }
                        onDelete(ticket.id);
                      }}
                    >
                      <svg
                        viewBox="0 0 24 24"
                        width="16"
                        height="16"
                        aria-hidden="true"
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="1.5"
                        strokeLinecap="round"
                        strokeLinejoin="round"
                      >
                        <path d="M4 7h16" />
                        <path d="M10 11v6M14 11v6" />
                        <path d="M6 7l1 12a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1l1-12" />
                        <path d="M9 7V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v3" />
                      </svg>
                    </button>
                  )}
                  {isShaping && onAccept !== undefined && (
                    <button
                      type="button"
                      data-role="detail-accept"
                      onClick={() => {
                        onAccept(ticket.id);
                      }}
                    >
                      Accept
                    </button>
                  )}
                </div>
              )}
            </div>
          )}
        </Drawer.Content>
      </Drawer.Portal>
    </Drawer.Root>
  );
}
