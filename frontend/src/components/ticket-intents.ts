// What the client is allowed to DO to a ticket, and how a failed write recovers
// — in one hook, for every screen that opens a ticket (shell-architecture plan,
// layer L1).
//
// Before this module the same seven callbacks were written twice, once in
// `PrimaryScreen` and once in `KanbanScreen`: five with byte-identical bodies
// (only the comments differed) and two — accept and delete — differing by
// exactly one line. This is the whole of the difference, and it is now a
// parameter rather than a fork.
//
// **No DOM, no feed knowledge, and no store lookups.** Everything it needs is
// injected, so it is testable by handing it a fake and calling it — see
// `ticket-intents.test.ts`. In particular:
//
//   * `refreshBoard` is a parameter rather than a `useBoardStore()` call, which
//     keeps this module free of any provider dependency at all.
//   * The feed's optimistic card hides are INJECTED (plan §7, decided
//     2026-08-08: injection over an ambient `useOptionalFeedStore()`).
//     `PrimaryScreen` passes the feed store's `acceptProposal` /
//     `deleteTicketCard`; `KanbanScreen` passes NEITHER, because `/kanban`
//     deliberately does not mount `FeedProvider` — a board view costs no
//     `GET /api/feed`. That omission is visible at the call site, on purpose:
//     whether a route has a feed must be legible AT that route, and an ambient
//     lookup would hide it inside this file where the next author will not look.
//     **Do not add a route by which this module reaches for a feed store.**
//     Reopen that only under the condition in plan §7.5 — five or six screens
//     each wiring the same two hides by hand — which is a count, not a feeling.
//
// Why some of these go through the brain and some do not is the standing
// distinction, unchanged by the move: accept/delete/poke express INTENT and are
// routed through the brain (D5), while the sandbox option, the two sandbox
// overrides and the text edit are direct writes. Each direct write earns it for
// its own reason — see `TicketDetail`'s "two direct writes" section — and each
// recovers the same way, by refreshing the board so the sheet snaps back to the
// truth at once rather than waiting out its optimistic time-box.
import { useCallback, useMemo } from 'react';
import {
  acceptTicket,
  deleteTicket,
  editTicketText,
  killTicketSandbox,
  postMessage,
  reassignTicketSandbox,
  setTicketSandbox,
} from '@/transport/transport';
import type { TicketTextEdit } from '@/components/TicketDetail';

export interface TicketActionsOptions {
  /** Re-fetch the board. Called when a direct write is refused, so the sheet
   * shows the truth immediately instead of waiting out its optimistic
   * time-box — including the 409s a ticket that moved mid-sheet returns. */
  refreshBoard: () => void;
  /** Optimistically hide the accepted ticket's proposal card, so the tap feels
   * instant. Passed by screens that HAVE a feed; omitted by those that don't
   * (see the header — this is plan §7's injection decision). */
  onAcceptOptimistic?: ((ticketId: string) => void) | undefined;
  /** Optimistically hide the deleted ticket's board-derived card — a proposal,
   * or a blocked ticket's blocker card. Same gating as `onAcceptOptimistic`. */
  onDeleteOptimistic?: ((ticketId: string) => void) | undefined;
}

/** The seven ticket intents, in the prop names the views take them under. Every
 * callback is referentially stable for as long as its inputs are, because
 * `FeedCardItem` and `TicketDetail` re-render off callback identity. */
export interface TicketActions {
  onAccept: (ticketId: string) => void;
  onDelete: (ticketId: string) => void;
  onPoke: (ticketId: string) => void;
  onSetKeepSandbox: (ticketId: string, keep: boolean) => void;
  onKillSandbox: (ticketId: string) => void;
  onReassignSandbox: (ticketId: string) => void;
  onEditText: (ticketId: string, patch: TicketTextEdit) => void;
}

export function useTicketActions({
  refreshBoard,
  onAcceptOptimistic,
  onDeleteOptimistic,
}: TicketActionsOptions): TicketActions {
  const onAccept = useCallback(
    (ticketId: string): void => {
      // Hide the proposal card first, where a screen has one to hide, so the tap
      // feels instant; that hide is time-boxed and self-heals if the accept never
      // lands (feed store).
      onAcceptOptimistic?.(ticketId);
      // Tap-accept routes straight to the accept endpoint (08 §5 / D6); the
      // resulting board + feed transitions come back over the stream.
      void acceptTicket(ticketId);
    },
    [onAcceptOptimistic],
  );

  const onDelete = useCallback(
    (ticketId: string): void => {
      // The blocked-delete confirm (D4) happens in the detail sheet, which knows
      // the ticket's state — by the time we're called the user has confirmed.
      onDeleteOptimistic?.(ticketId);
      // Deleting routes through the brain (delete_ticket, D5), same as accept; the
      // resulting board + feed removal comes back over the stream. A blocked
      // delete also releases the ticket's worker board-side. Fire-and-forget.
      void deleteTicket(ticketId);
    },
    [onDeleteOptimistic],
  );

  const onPoke = useCallback((ticketId: string): void => {
    // A manual poke nudges a stalled agent to continue. The client can't (and by
    // D5 mustn't) command the agent directly — send_to_agent is a brain tool — so
    // the poke is expressed as a human message naming the ticket. The brain, which
    // loads full board state per event, resolves the ticket and decides to
    // send_to_agent(id, "continue"); the resulting activity returns over the
    // stream. Fire-and-forget like accept: a dropped poke just means the nudge
    // didn't land, and the user can tap again.
    void postMessage(`Poke the agent on ticket ${ticketId} to continue.`);
  }, []);

  const onSetKeepSandbox = useCallback(
    (ticketId: string, keep: boolean): void => {
      // The per-ticket sandbox option is a setting on the ticket, not a board
      // transition, so — unlike accept/delete/poke — it does not go through the
      // brain: it writes the flag directly and the board.updated that write emits
      // brings the new value back over the stream. Fire-and-forget: the sheet
      // shows the choice at once and time-boxes it, so a dropped write just means
      // the checkmark clears and the user can tap again.
      void setTicketSandbox(ticketId, keep).catch(() => {
        refreshBoard();
      });
    },
    [refreshBoard],
  );

  const onKillSandbox = useCallback(
    (ticketId: string): void => {
      // The manual override for a wedged or corrupted sandbox. Like the sandbox
      // option — and unlike accept/delete/poke — it does not go through the
      // brain: the whole reason it exists is that waiting for the orchestrator
      // to notice is the problem, so routing it through an LLM turn would put
      // that wait straight back. The board's agent.release does the work and the
      // resulting board.updated brings the slot's new status over the stream.
      // A refused kill (409 — the ticket lost its sandbox while the sheet was
      // open) snaps the sheet back to the truth at once.
      void killTicketSandbox(ticketId).catch(() => {
        refreshBoard();
      });
    },
    [refreshBoard],
  );

  const onReassignSandbox = useCallback(
    (ticketId: string): void => {
      // The recovery counterpart to the kill, and a direct write for the same
      // reason. The board rebinds the ticket and re-briefs the new agent itself,
      // so there is nothing to send here. A refusal (409 — every slot went busy
      // between the render and the tap) refreshes the board, which is also what
      // drops the item from the menu.
      void reassignTicketSandbox(ticketId).catch(() => {
        refreshBoard();
      });
    },
    [refreshBoard],
  );

  const onEditText = useCallback(
    (ticketId: string, patch: TicketTextEdit): void => {
      // A direct text edit is the one place the user's own words must land on
      // the board untouched, so — like the sandbox option and unlike
      // accept/delete/poke — it does not go through the brain: routing a
      // correction through an LLM pass is exactly the drift this affordance
      // exists to remove. Fire-and-forget: the sheet shows the typed text at
      // once and time-boxes it, so a dropped write just means the old wording
      // comes back and the user can edit again. Refresh on failure so it snaps
      // back to the truth immediately rather than on the time-box — including
      // the 409 the server returns if the ticket left the backlog while the
      // sheet was open.
      void editTicketText(ticketId, patch).catch(() => {
        refreshBoard();
      });
    },
    [refreshBoard],
  );

  return useMemo(
    () => ({
      onAccept,
      onDelete,
      onPoke,
      onSetKeepSandbox,
      onKillSandbox,
      onReassignSandbox,
      onEditText,
    }),
    [onAccept, onDelete, onPoke, onSetKeepSandbox, onKillSandbox, onReassignSandbox, onEditText],
  );
}
