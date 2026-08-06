// Ticket detail sheet. Opening a board card slides it into view from the edge
// the shell asks for — up from the bottom on a phone, in from the right at a
// desk (see `placement`) — showing the ticket's full
// record — everything the card elides: the complete body, priority, timestamps,
// id, and (when blocked) the full blocked reason. It is read-only inspection
// over the read-only board (D5) as far as the ticket's *state* goes: Accept,
// Delete and Poke all only express intent, which the caller routes through the
// brain. It writes exactly two things directly, both of them the user's own
// input rather than a board transition:
//
//   • the per-ticket sandbox option (`onSetKeepSandbox`) — a setting on the
//     ticket; and
//   • the ticket's own title/body text (`onEditText`) — pressing the rendered
//     body turns the title and body into fields the user types in, in place.
//
// It also carries the two manual sandbox overrides (`onKillSandbox`,
// `onReassignSandbox`), which are direct writes for a third reason again: they
// exist precisely to let the user reach past the orchestrator when a sandbox is
// wedged or its working tree is corrupted, so routing them through the brain
// would reinstate the wait they were built to remove. They act on the sandbox
// behind the ticket's slot rather than on the ticket's place on the board, which
// is why they are not board transitions at all.
//
// All three live together behind ONE gear beside the lifecycle badge
// (`TicketDetailSandboxMenu`) rather than as controls in the sheet's body: they
// are decisions about the *workspace*, not about the work, and the row of
// buttons and explanatory paragraphs they used to be pushed the ticket's own
// text down the screen on every visit to serve a choice made on almost none.
//
// The text edit is deliberately NOT routed through the brain, and for a sharper
// reason than the sandbox toggle: describing a wording change out loud and
// letting the brain rewrite the ticket is exactly what drifts from what the user
// meant, so a direct edit exists precisely to put the typed text on the board
// verbatim. It is offered only while the ticket is still in the backlog
// (EDITABLE_STATES) — past that, the text is what the agent was briefed with.
//
// The slide-in entrance + native rubber-band overscroll and drag-to-dismiss come
// from `vaul` — the standard React drawer/sheet, adopted with explicit user
// sign-off waiving the former blanket no-library rule (07 D4). Vaul owns
// dismissal entirely: dragging the sheet back past the threshold, clicking the
// scrim, and pressing Escape all route through `onOpenChange(false)` →
// `onClose`, so this component adds none of that by hand — dismiss stays
// low-friction, never a trap (07 §7–§8). That is also why the header carries
// **no × button**: with three dismiss paths already there, the one piece of
// chrome it bought was a fourth — at the cost of a column of the header the
// title had to be shrunk to clear. The title now takes the full width and reads
// at its natural size, and the sheet is dismissed the way every sheet is.
//
// WHICH edge it comes from is the `placement` prop: `'bottom'` (the default) is
// the phone's sheet, `'right'` is the desk's side panel. It is one prop rather
// than two components because everything above — the record, the actions, the
// direct writes — is the same sheet either way; only the edge it is anchored to
// differs (see the prop's doc for why the desk's is a different edge).
// `MouseEvent` and `PointerEvent` are aliased on import: the body's click and
// press handlers take React's synthetic events, and the unaliased names would
// read as the DOM globals.
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type JSX,
  type MouseEvent as ReactMouseEvent,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
} from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { Drawer } from 'vaul';
import type { Ticket } from '@/components/TicketCard';
import { TicketDetailSandboxMenu } from '@/components/TicketDetailSandboxMenu';
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
  /** When provided, the gear menu carries the per-ticket **sandbox option** —
   * "Save sandbox when done", a toggle reading the ticket's own `keep_sandbox`.
   * Saving a ticket's sandbox stops the board releasing its worker when the
   * ticket leaves Developing, so the workspace survives and an agent can keep
   * working in that same sandbox across turns.
   * Unlike Accept/Delete/Poke this is a setting rather than an intent, so the
   * caller writes it directly (POST /api/tickets/{id}/sandbox) rather than routing
   * it through the brain, and the sheet stays open after the toggle — the user may
   * well flip it and keep reading. Offered on every lifecycle state but done: the
   * choice matters before the sandbox exists (a proposal the user already knows
   * they want to keep working in) just as much as while it's running, but on a
   * finished ticket the moment it decides has passed. Omitted → no toggle; with
   * the two overrides also unwired there is no gear at all, so a read-only sheet
   * is unchanged. */
  onSetKeepSandbox?: ((ticketId: string, keep: boolean) => void) | undefined;
  /** When provided on a ticket that has a sandbox (working|blocked), the gear menu
   * carries **Re-create sandbox** — destroy the workspace this ticket's agent is
   * in so its slot comes back with a fresh one. This is the manual override for a
   * wedged or corrupted sandbox: the thing that previously meant waiting for the
   * orchestrator to notice and clean up. Like the sandbox toggle (and unlike
   * Accept/Delete/Poke) the caller writes it directly rather than routing it
   * through the brain — an override that waits on an LLM turn is not an override.
   * Destructive and irreversible, so the menu gates it behind a confirm naming
   * what is lost. Omitted → no item. */
  onKillSandbox?: ((ticketId: string) => void) | undefined;
  /** When provided on a ticket that has a sandbox (working|blocked), the gear menu
   * carries **Move to free sandbox** — the recovery counterpart to Re-create: the
   * ticket is bound to a different free sandbox and the agent there is briefed
   * with the ticket's work order from scratch, while the old workspace is thrown
   * away. Use it when the workspace is beyond saving and the work should simply
   * start again somewhere clean. Also a direct write, also confirm-gated (it
   * discards whatever the current agent had done). Omitted → no item. */
  onReassignSandbox?: ((ticketId: string) => void) | undefined;
  /** Whether there is a free sandbox to move this ticket to, from the board
   * snapshot's `worker_free`. False drops the Move item from the menu entirely
   * rather than showing a dead one: with every slot busy there is nowhere to go,
   * the server would only answer 409, and inside a menu an unavailable line is
   * pure noise — Re-create is the option left. Defaults true (unknown capacity →
   * offer it and let the server be the judge). */
  canReassign?: boolean;
  /** The live session status of this ticket's sandbox, from the board snapshot's
   * `agents` join — the same lookup that feeds `agentIdle`, shown as the gear
   * menu's first line. It is what makes Re-create a considered action rather than
   * a blind one: an `errored` or `stopped` sandbox is the case the control exists
   * for, and a `building` one warns that a turn is being cut short. Omitted → the
   * line says the sandbox isn't reporting, which is itself a signal. */
  sandboxStatus?: string | undefined;
  /** When provided, the rendered body of a ticket still in EDITABLE_STATES
   * becomes the **edit** affordance itself: pressing the text turns the title
   * and body into a text field and a textarea, in place, where the body was
   * — there is no separate pencil, because the words the user wants to change
   * are the obvious thing to reach for. Saving calls this with only the fields
   * that actually changed. Unlike Accept/Delete/Poke it carries no intent for
   * the brain to interpret: the caller writes the text straight to the board
   * (POST /api/tickets/{id}/text), because an LLM pass between the user and
   * their own words is the drift this affordance exists to remove. Like the
   * sandbox switch the sheet stays open afterwards — the user saves and reads
   * the result. Omitted → the body is inert text, so a read-only sheet is
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
  /** Whether a voice session is live on this ticket right now — the mic is up, or
   * there are spoken words waiting to be sent. It rearranges the footer, and only
   * the footer:
   *
   *   • the voice cluster (`voiceControl`) crosses from the bottom-LEFT to the
   *     trailing group, so the mic sits beside the Send and × that act on what it
   *     is hearing rather than across the row from them; and
   *   • **Accept is withheld** for the duration. Its slot is the one Send takes,
   *     and the primary decision about a proposal has no business under the thumb
   *     of someone mid-sentence. It returns, in its normal place, the moment the
   *     session ends (sent, discarded, or the mic stopped).
   *
   * Delete and Poke are untouched — they are quiet icon secondaries either way.
   *
   * It is a plain boolean, passed in, for the same reason `voiceControl` and
   * `transcript` are nodes: this component stays free of the voice store. The
   * caller gets it from `TicketDetailVoiceActions`'s `onActiveChange`, which
   * reports a *reading* rather than a transcript — so the screen above re-renders
   * when the footer's shape changes, not once a word. Defaults false, so a
   * read-only sheet (and every caller that wires no voice) is unchanged. */
  voiceActive?: boolean;
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
  /** Which edge the sheet is anchored to — and so which direction it slides in
   * from, which axis a drag dismisses along, and where its closed position is.
   * The value is handed straight to vaul as its `direction`, so all three follow
   * from this one prop and nothing here (or in CSS) restates them.
   *
   * `'bottom'` is the default and the phone's reading: a classic mobile sheet
   * that rises from the bottom edge across the full width (07 §7). The desktop
   * shell passes `'right'` instead, because on a window that reading is wrong —
   * a sheet that takes the middle of the screen hides the feed and the working
   * strip the user opened the ticket to check against. Anchored to the right
   * edge at full height it is a panel beside the work rather than over it, and
   * the rail and most of the feed stay on screen behind it.
   *
   * The matching geometry is CSS, and it is keyed off `data-shell` on <body>
   * (published by the same desktop shell that passes this prop) rather than off
   * a `data-placement` attribute here: the sheet portals to document.body, so
   * `body[data-shell='desktop'] …` is the one selector that can reach it — see
   * the "detail, beside the feed" section of DesktopScreen.css. */
  placement?: 'bottom' | 'right';
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
 * the sheet's body is inert text. This set is the single seam for widening the
 * affordance later — widen the board's precondition with it. */
const EDITABLE_STATES = new Set<Ticket['state']>(['shaping', 'ready']);

/** The lifecycle states that actually have a sandbox behind them, and so the
 * only ones the gear menu's Re-create / Move items appear on. It mirrors the
 * board's own precondition (a worker is bound iff the ticket is working or
 * blocked) rather than restating a client-side opinion — on any other state the
 * server refuses with a 409, so offering the item would only produce an error. */
const SANDBOX_CONTROL_STATES = new Set<Ticket['state']>(['working', 'blocked']);

/** How the sandbox's live session status reads at the head of the gear menu. The
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

/** How far a pointer may travel from where it went down and still be a press on
 * the body rather than a drag through it. Past this the gesture is a scroll (or
 * a swipe at the sheet itself): the press wash comes off and the release that
 * ends it does not open the editor. Matches SwipeToDismiss's engage slop — the
 * same question, asked of the same fingers. */
const TAP_SLOP_PX = 8;

/** How long a pointer must rest on the body before the press wash appears.
 *
 * The wash cannot go on at pointerdown, because a scroll starts with a
 * pointerdown too: the browser only knows the finger is scrolling once it has
 * moved, so a wash painted immediately is a wash that flickers under every
 * scroll that happens to start on the words. Waiting a beat first is what the
 * native platforms do (iOS's `delaysContentTouches`, Android's tap timeout) and
 * it costs a genuine press nothing — a finger that means to press is still
 * there when the beat is up. */
const PRESS_FEEDBACK_DELAY_MS = 100;

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
  voiceActive = false,
  transcript,
  surface = 'debug',
  placement = 'bottom',
}: TicketDetailProps): JSX.Element {
  // Which affordances the sheet's footer carries is decided purely by lifecycle
  // state, so the caller can't wire a nonsensical one:
  //  • every state     → the mic (when wired): the unified communication surface
  //                      (08 §5) — the user can start talking to the brain from any
  //                      ticket. Replaces the old blocked-only "Talk to unblock"
  //                      button so all ticket types share one interface. While a
  //                      voice session is live it brings Send and × with it to the
  //                      row's trailing end (`voiceActive`).
  //  • shaping         → Accept (when wired, and no voice session live): the
  //                      proposal click-through (08 §5) — accepting is what moves a
  //                      shaped proposal into the pull, so it only makes sense here.
  //                      Every later state has already been accepted, so the button
  //                      is gone.
  //  • working|blocked → Poke (when wired): a manual nudge to continue for a
  //                      stalled agent, routed through the brain (never a direct
  //                      agent command, D5). On a working ticket it only shows once
  //                      the agent is idle (`agentIdle`) — never mid-turn; on a
  //                      blocked ticket it always shows.
  //  • done            → no action but the mic; the header badge already says "Done".
  // The footer branches below narrow on the callbacks directly (not derived
  // booleans) so TypeScript knows they're defined inside the handler — no
  // optional chain, which the lint gate rejects (mirrors FeedCardItem).
  // The gear menu's save toggle renders the user's choice at once, then hands
  // back to the board snapshot: `pendingKeep` is null whenever the snapshot is
  // authoritative. It clears as soon as the snapshot agrees, and otherwise on the
  // time-box — so a write that never lands can't leave the checkmark on a lie.
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
  // A live voice session on this ticket — only meaningful when a mic is actually
  // wired, so a caller can't put the footer into the speaking arrangement with
  // nothing to speak into. While it holds, the voice cluster moves to the
  // trailing group and Accept stands down (see the prop's doc).
  const inVoiceMode = voiceControl !== undefined && voiceActive;
  const canAccept = isShaping && onAccept !== undefined;
  // The voice cluster — the mic and, while it is live, its Send and × — wired only
  // on the primary screen (a read-only sheet leaves it undefined). At rest it is
  // the bottom-left half of the pair 08 §5 calls for, opposite the trailing
  // Accept/Poke; while a session is live it crosses over to join them
  // (`inVoiceMode` above). It shows on every ticket state (the unified
  // communication surface — start talking from any ticket), so it is gated only on
  // being wired; Delete shows in any DELETABLE_STATES state (shaping or blocked).
  const showVoice = voiceControl !== undefined;
  const canDelete = DELETABLE_STATES.has(ticket.state) && onDelete !== undefined;
  // Whether the sheet is in edit mode, and whether it can be entered at all —
  // a backlog ticket whose text the board will still accept, with the write
  // wired. A derived boolean is safe here, unlike the footer's callbacks: what
  // it guards only seeds local draft state, and `onEditText` isn't called until
  // Save, which narrows it itself.
  const editing = draft !== null;
  const canEditText = EDITABLE_STATES.has(ticket.state) && onEditText !== undefined;
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
  // Whether there is a sandbox behind this ticket to override at all — the two
  // destructive items are offered only on working|blocked, mirroring the board's
  // own "a worker is bound" precondition rather than restating a client-side
  // opinion. The save toggle is not gated on it: that choice predates the
  // sandbox.
  const hasSandbox = SANDBOX_CONTROL_STATES.has(ticket.state);
  // The gear appears when any of the three items would — the same shape as every
  // other affordance here, so a read-only sheet is unchanged. It is suppressed
  // in two cases:
  //  • mid-edit: what happens to the workspace is a different kind of decision
  //    from the wording of the ticket, and has no place in the middle of typing.
  //  • on a done ticket: the work is over, so every item behind the gear is
  //    spent — the two overrides have no sandbox to act on (done is not in
  //    SANDBOX_CONTROL_STATES), and the save toggle decides what happens to a
  //    sandbox when the ticket finishes, which it already has. A gear whose only
  //    item can no longer change anything is worse than no gear.
  const showSandboxMenu =
    !editing &&
    ticket.state !== 'done' &&
    (onSetKeepSandbox !== undefined ||
      (hasSandbox && (onKillSandbox !== undefined || onReassignSandbox !== undefined)));
  // The menu's status line reads the sandbox's own session state, not the
  // ticket's board column. An unknown status is reported as such rather than
  // guessed at: "not reporting" is itself the signal that something is wrong with
  // the sandbox, which is exactly when these controls matter.
  const sandboxStatusLabel =
    sandboxStatus === undefined
      ? 'not reporting'
      : (SANDBOX_STATUS_LABELS[sandboxStatus] ?? sandboxStatus);
  // The toggle's write, bound here rather than in the menu so the optimistic
  // value stays with the state that holds it. Built inside the ternary so
  // TypeScript narrows the callback to defined — the same reason every button
  // below re-checks its own prop instead of a derived boolean.
  const setKeepSandbox =
    onSetKeepSandbox === undefined
      ? undefined
      : (id: string, keep: boolean): void => {
          setPendingKeep(keep);
          onSetKeepSandbox(id, keep);
        };

  // Entering edit mode puts the caret in the *body*, because the body is what
  // the user pressed to get here — and at the end of the text, so pressing a
  // paragraph to add a line doesn't drop the caret in front of everything
  // already written. A ref callback rather than `autoFocus` since it has to
  // place the caret too, and `useCallback` so its identity is stable: an inline
  // arrow would be a new ref on every render, re-running this (and yanking the
  // caret back to the end) after every keystroke.
  const focusBodyEnd = useCallback((node: HTMLTextAreaElement | null) => {
    if (node === null) {
      return;
    }
    node.focus();
    node.setSelectionRange(node.value.length, node.value.length);
  }, []);

  // The press on the body, tracked by hand so that scrolling through the ticket
  // never reads as reaching for the editor. The body is inside the sheet's own
  // overflow region, so most fingers that land on it are on their way past —
  // and both halves of the affordance have to tell the difference:
  //
  //  • the wash: `pressed` drives [data-pressed], the touch half of the CSS
  //    (the pointer half is :hover, which is gated behind `hover: hover` there
  //    precisely so a phone's *emulated* hover can't paint it mid-scroll). It
  //    goes on a beat after the finger lands and comes straight off the moment
  //    the gesture turns into a drag.
  //  • the entry: `draggedRef` remembers that it did, so the click that ends a
  //    drag is not taken for a tap. A touch scroll usually swallows its click,
  //    but a drag on the sheet itself (vaul's dismiss) does not.
  //
  // Refs, not state, for the gesture itself: nothing on screen depends on where
  // the finger is, only on the verdict, so a re-render per pointermove would be
  // pure cost.
  const [pressed, setPressed] = useState(false);
  const pressOriginRef = useRef<{ id: number; x: number; y: number } | null>(null);
  const pressTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const draggedRef = useRef(false);

  /** Drop the press: cancel the pending wash (or take it off, if it is already
   * on) and stop tracking the pointer. Every way a press can end routes through
   * here — released, dragged away, cancelled by the browser taking the gesture
   * for a scroll, or the sheet unmounting mid-press. */
  const endPress = useCallback((): void => {
    pressOriginRef.current = null;
    if (pressTimerRef.current !== null) {
      clearTimeout(pressTimerRef.current);
      pressTimerRef.current = null;
    }
    setPressed(false);
  }, []);

  // A press held while the ticket leaves EDITABLE_STATES (or the sheet closes)
  // would otherwise leave the timer to fire into an unmounted component.
  useEffect(() => {
    return () => {
      if (pressTimerRef.current !== null) {
        clearTimeout(pressTimerRef.current);
        pressTimerRef.current = null;
      }
    };
  }, []);

  /** A pointer lands on the body. Nothing shows yet — the wash is armed for a
   * beat's time, and only a finger still resting here when it fires gets one.
   * A secondary mouse button is not a press at all (it opens the context menu),
   * so it is left alone. */
  function beginPress(event: ReactPointerEvent<HTMLElement>): void {
    if (event.pointerType === 'mouse' && event.button !== 0) {
      return;
    }
    draggedRef.current = false;
    pressOriginRef.current = { id: event.pointerId, x: event.clientX, y: event.clientY };
    if (pressTimerRef.current !== null) {
      clearTimeout(pressTimerRef.current);
    }
    pressTimerRef.current = setTimeout(() => {
      pressTimerRef.current = null;
      setPressed(true);
    }, PRESS_FEEDBACK_DELAY_MS);
  }

  /** The pointer moved while down. Under the slop it is the ordinary jitter of
   * a finger held still; past it the gesture is a drag — a scroll through the
   * ticket, or a pull at the sheet — and this stops being a press for good. */
  function trackPress(event: ReactPointerEvent<HTMLElement>): void {
    const origin = pressOriginRef.current;
    if (origin?.id !== event.pointerId) {
      return;
    }
    if (
      Math.abs(event.clientX - origin.x) < TAP_SLOP_PX &&
      Math.abs(event.clientY - origin.y) < TAP_SLOP_PX
    ) {
      return;
    }
    draggedRef.current = true;
    endPress();
  }

  /** The gesture was taken away — the browser claimed it for a scroll
   * (pointercancel, which is how a touch scroll ends), or the pointer left the
   * body still down. Either way it was not a press on these words. */
  function abandonPress(): void {
    if (pressOriginRef.current === null) {
      return;
    }
    draggedRef.current = true;
    endPress();
  }

  // The body as it reads on screen. An editable ticket with nothing written yet
  // gets a prompt in place of the (empty) Markdown: the body is the only way
  // into edit mode now, so there always has to be something there to press. A
  // ticket past editing with an empty body still renders as empty — there is
  // nothing to invite.
  const renderedBody =
    canEditText && shownBody.trim() === '' ? (
      <p data-role="detail-body-placeholder">Add a description</p>
    ) : (
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{shownBody}</ReactMarkdown>
    );

  /** Enter edit mode, seeded from what is on screen (the optimistic text while
   * a save is in flight, otherwise the board's). Both ways in call this: the
   * press on the body itself, and the keyboard control that stands in for it. */
  function beginEdit(): void {
    setDraft({ title: shownTitle, body: shownBody });
  }

  /** The press on the rendered body — the whole edit affordance now, which is
   * why it has to leave alone the three presses inside a body that already mean
   * something else:
   *   • a click that ends a drag is a scroll through the ticket (or a pull at
   *     the sheet), not a tap on the words it happened to start on.
   *   • a link in the Markdown is still a link. Following a reference is not a
   *     request to rewrite the sentence it sits in.
   *   • selecting words to quote ends in a click on the region, which would
   *     otherwise swap the selection out for a textarea the instant the user
   *     let go — so reading and copying a ticket never drops into edit mode. */
  function editFromBody(event: ReactMouseEvent<HTMLElement>): void {
    // Read and clear: the verdict belongs to the gesture that just ended, and
    // leaving it set would make the next click answer for that one instead.
    const dragged = draggedRef.current;
    draggedRef.current = false;
    if (dragged) {
      return;
    }
    if (event.target instanceof Element && event.target.closest('a') !== null) {
      return;
    }
    const selection = window.getSelection();
    if (selection !== null && selection.toString() !== '') {
      return;
    }
    beginEdit();
  }

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
      // The anchored edge, straight from `placement`: the phone's sheet rises
      // from the bottom (07 §7), the desk's panel slides in from the right
      // (13 D7a). Vaul derives the entrance, the closed transform and the drag
      // axis from it, so this is the only place the direction is stated.
      direction={placement}
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
              grabber is the first child, above the header. The desk's
              right-anchored panel has no edge to grab and hides it in CSS,
              rather than branching the markup: one sheet serves both shells. */}
          <Drawer.Handle data-role="ticket-detail-grabber" />

          <header data-role="ticket-detail-header">
            {/* Title, gear and lifecycle badge stack in one left-aligned column:
                the header has no second column at all now (no × — see the file
                header), so the title gets the sheet's full width and every row
                below it starts on the title's own left edge. */}
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
                  onChange={(event) => {
                    setDraft({ title: event.target.value, body: draft.body });
                  }}
                />
              )}
              {/* The status row, directly under the title: the sandbox gear and
                  the lifecycle badge, side by side. They share a line because
                  they answer the two questions asked at a glance — what can be
                  done about the workspace behind this ticket, and what is
                  happening to it — and because the gear needs a home that exists
                  on every lifecycle state, including the ones that wear no badge.
                  The gear leads the row so it sits on the title's own left edge
                  rather than off in the corner the × used to hold. It is skipped
                  entirely when neither has anything to show. */}
              {(statusLabel !== undefined || showSandboxMenu) && (
                <div data-role="ticket-detail-status-row">
                  {/* Every sandbox decision for this ticket, behind one gear. Each
                      item is gated by whether its callback arrives: the two
                      destructive ones only on a ticket that has a sandbox, and Move
                      only while a free slot exists to move it to (`canReassign`) —
                      an item the server would refuse is worse than no item. */}
                  {showSandboxMenu && (
                    <TicketDetailSandboxMenu
                      ticketId={ticket.id}
                      keepSandbox={pendingKeep ?? serverKeep}
                      onSetKeepSandbox={setKeepSandbox}
                      onKillSandbox={hasSandbox ? onKillSandbox : undefined}
                      onReassignSandbox={hasSandbox && canReassign ? onReassignSandbox : undefined}
                      sandboxStatusLabel={hasSandbox ? sandboxStatusLabel : undefined}
                    />
                  )}
                  {/* The lifecycle badge: a dot + word that names the ticket's
                      state at a glance (In progress / Blocked / Done), each in its
                      own colour. Only the states that carry a signal show one;
                      shaping/ready wear none. Keyed on data-state (not Radix's own
                      data-state, which lives on the panel) for its per-state
                      colour. */}
                  {statusLabel !== undefined && (
                    <span data-role="ticket-detail-status" data-state={ticket.state}>
                      <span data-role="ticket-detail-status-dot" aria-hidden="true" />
                      {statusLabel}
                    </span>
                  )}
                </div>
              )}
            </div>
            {/* No chrome column beside the heading: the body itself is the way
                into edit mode (see the scroll region below), and dismissal is
                Vaul's — drag, scrim, Escape — so the header is the heading and
                nothing else. */}
          </header>

          {/* The scroll region: the block message and the Markdown body live
              together inside the one overflowing area, so a long block message
              scrolls with the body instead of being clipped by the panel's
              overflow: hidden. Both sit under the pinned header/meta.

              While editing, the whole region becomes the body textarea: the
              rendered Markdown is what the user is replacing. The blocked reason
              never appears here either — a blocked ticket is not editable in the
              first place. */}
          <div data-role="ticket-detail-body" data-editing={editing ? 'true' : undefined}>
            {draft !== null ? (
              <textarea
                data-role="detail-edit-body"
                aria-label="Description"
                value={draft.body}
                data-vaul-no-drag
                ref={focusBodyEnd}
                onChange={(event) => {
                  setDraft({ title: draft.title, body: event.target.value });
                }}
              />
            ) : (
              <>
                {ticket.state === 'blocked' && ticket.blocked_reason != null && (
                  <p data-role="detail-blocked-reason">{ticket.blocked_reason}</p>
                )}
                {/* The body — and, on an editable ticket, the edit affordance
                    itself: pressing the rendered Markdown replaces it with the
                    textarea in the same place, so the words never move to be
                    changed. The wrapper is a plain div, deliberately not a
                    <button> or a role="button": a button's contents are
                    announced as its label rather than as the document they are,
                    and the body is the very thing the sheet exists to show —
                    plus a body's Markdown may hold links, which cannot live
                    inside a button at all. The keyboard way in is the sibling
                    control below instead. A ticket past editing renders the
                    Markdown bare, exactly as before. */}
                {canEditText ? (
                  <>
                    <div
                      data-role="detail-body-edit-target"
                      // The press wash, and the guards that keep a scroll from
                      // wearing it: see beginPress/trackPress above. It is a
                      // data attribute rather than a class because everything
                      // else on this sheet is keyed the same way.
                      data-pressed={pressed ? 'true' : undefined}
                      onPointerDown={beginPress}
                      onPointerMove={trackPress}
                      onPointerUp={endPress}
                      onPointerCancel={abandonPress}
                      onPointerLeave={abandonPress}
                      onClick={editFromBody}
                    >
                      {renderedBody}
                    </div>
                    {/* The keyboard/assistive route, standing in for the press.
                        It is off-screen until focused (the skip-link shape)
                        rather than hidden outright: with the pencil gone this is
                        the only way in for anyone not using a pointer, so it has
                        to be tabbable — but it must not become a second visible
                        affordance beside the body it duplicates. */}
                    <button type="button" data-role="detail-body-edit-key" onClick={beginEdit}>
                      Edit description
                    </button>
                  </>
                ) : (
                  renderedBody
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
                          later state has already been accepted) — and only while
                          no voice session is live: mid-utterance its slot belongs
                          to Send (`voiceActive`).
              The voice cluster sits first (left) at rest and Poke/Delete after it;
              the state's primary action (Accept) stays rightmost, where flex-end
              makes it the most prominent. While the mic is up the cluster crosses
              to the end of the row and takes that slot instead, so the trailing
              group reads Send, ×, mic from the right edge inward. Every affordance
              here reads as a glyph only — the mic, the 👉 for Poke, the trash for
              Delete, the check for Accept — with no text label around any of them;
              Accept used to carry the word, and now carries it as an aria-label
              instead, so the footer is one row of icons in one treatment (the
              mic's — see the CSS) rather than a pill among glyphs. Each button
              narrows on its callback directly
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
                  {/* The voice cluster, in whichever of its two homes this moment
                  calls for — one element in one place in the DOM, moved by CSS
                  (`data-position`) rather than by being re-parented, so the mic
                  inside it is never unmounted and remounted mid-utterance.
                  `lead` is the resting reading: `margin-right: auto` pins it to
                  the row's left edge and pushes the state actions
                  (Poke/Delete/Accept) to the right. `trail` drops that margin and
                  orders it LAST, so the row reads (right to left) Send, ×, mic —
                  Send in the slot Accept has just vacated. Absent (a sheet without
                  voice) the row is byte-identical to the old flex-end footer. */}
                  {showVoice && (
                    <div
                      data-role="ticket-detail-voice-actions"
                      data-position={inVoiceMode ? 'trail' : 'lead'}
                    >
                      {voiceControl}
                    </div>
                  )}
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
                        width="22"
                        height="22"
                        aria-hidden="true"
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="2"
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
                  {/* Accept — withheld for the length of a voice session: the
                  trailing slot is Send's while the user is speaking, and the
                  headline decision about the proposal should not be sitting under
                  the thumb that is about to reach for it. It comes straight back
                  when the session ends. */}
                  {!inVoiceMode && isShaping && onAccept !== undefined && (
                    <button
                      type="button"
                      data-role="detail-accept"
                      aria-label="Accept"
                      onClick={() => {
                        onAccept(ticket.id);
                      }}
                    >
                      {/* A check in the same stroked idiom as the trash beside it,
                      on the same disc as the mic (see the CSS). The glyph is
                      aria-hidden, so the button's accessible name comes from
                      aria-label="Accept" — the word is gone from the screen but
                      not from the accessibility tree. */}
                      <svg
                        viewBox="0 0 24 24"
                        width="22"
                        height="22"
                        aria-hidden="true"
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="2"
                        strokeLinecap="round"
                        strokeLinejoin="round"
                      >
                        <path d="M20 6.5L9.5 17.5 4 12" />
                      </svg>
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
