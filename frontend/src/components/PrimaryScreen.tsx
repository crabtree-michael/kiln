// Primary screen (08 §6). Composes the feed + activity providers and bridges
// their stores into the presentational `PrimaryScreenView`. All markup, CSS, and
// the 08 §F selector surface live in the presentational tree; this file is only
// the wiring seam (stores → props, Accept → transport).
import { useCallback, type JSX } from 'react';
import { BoardProvider } from '@/stores/board-store';
import { FeedProvider } from '@/stores/feed-store';
import { ActivityProvider } from '@/stores/activity-store';
import { VoiceProvider } from '@/voice/voice-store';
import { useBoardStore } from '@/stores/board-context';
import { useFeedStore } from '@/stores/feed-context';
import { useActivityStore } from '@/stores/activity-context';
import { useNotificationMode } from '@/stores/use-notification-mode';
import { useWebPush } from '@/stores/use-web-push';
import { usePresence } from '@/stores/use-presence';
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
import { PrimaryScreenView } from '@/components/PrimaryScreenView';
import { ProjectSwitcher } from '@/components/ProjectSwitcher';
import { useKeyboardViewport } from '@/components/use-keyboard-viewport';

function PrimaryScreenBody(): JSX.Element {
  // Keep the screen column matched to the visible viewport while the keyboard is
  // open, so the dock stays docked above it rather than sliding off-screen.
  useKeyboardViewport();

  // Report foreground presence so the backend withholds a duplicate Web Push
  // while this tab is visible (02 §10 push dedup). No-ops until notifications
  // are enabled (no subscription ⇒ nothing to suppress).
  usePresence();

  const {
    feed,
    connectionState,
    lastSeenId,
    hasMoreHistory,
    loadingMoreHistory,
    loadMoreHistory,
    refreshFeed,
    acceptProposal,
    deleteTicketCard,
    dismissCard,
    dismissAll,
  } = useFeedStore();
  const { board, refreshBoard, refreshing } = useBoardStore();
  const { thinking, toasts, dismiss, setToastExpanded } = useActivityStore();
  const { mode: notificationMode, setMode: setNotificationMode } = useNotificationMode();
  const { status: pushStatus, enable: enablePush, disable: disablePush } = useWebPush();

  const onAccept = useCallback(
    (ticketId: string): void => {
      // Optimistically drop the proposal card so the tap feels instant; the hide
      // is time-boxed and self-heals if the accept never lands (feed store).
      acceptProposal(ticketId);
      // Tap-accept routes straight to the accept endpoint (08 §5 / D6); the
      // resulting board + feed transitions come back over the stream.
      void acceptTicket(ticketId);
    },
    [acceptProposal],
  );

  const onDelete = useCallback(
    (ticketId: string): void => {
      // Optimistically drop the ticket's board-derived card (a proposal, or a
      // blocked ticket's blocker card) so the tap feels instant; the hide is
      // time-boxed and self-heals if the delete never lands (feed store). The
      // blocked-delete confirm (D4) happens in the detail sheet, which knows the
      // ticket's state — by the time we're called the user has confirmed.
      deleteTicketCard(ticketId);
      // Deleting routes through the brain (delete_ticket, D5), same as accept; the
      // resulting board + feed removal comes back over the stream. A blocked
      // delete also releases the ticket's worker board-side. Fire-and-forget.
      void deleteTicket(ticketId);
    },
    [deleteTicketCard],
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
      // the switch snaps back and the user can tap again. Refresh the board on
      // failure so it snaps back to the truth immediately rather than on the
      // time-box.
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
      // Refresh on failure so a refused kill (409 — the ticket lost its sandbox
      // while the sheet was open) snaps the sheet back to the truth at once.
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
      // re-disables the button.
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
      // comes back and the user can edit again. Refresh the board on failure so
      // it snaps back to the truth immediately rather than on the time-box —
      // including the 409 the server returns if the ticket left the backlog
      // while the sheet was open.
      void editTicketText(ticketId, patch).catch(() => {
        refreshBoard();
      });
    },
    [refreshBoard],
  );

  return (
    <PrimaryScreenView
      brand={<ProjectSwitcher />}
      feed={feed}
      board={board}
      connectionState={connectionState}
      thinking={thinking}
      toasts={toasts}
      onDismiss={dismiss}
      onToastExpandedChange={setToastExpanded}
      onAccept={onAccept}
      onDelete={onDelete}
      onPoke={onPoke}
      onSetKeepSandbox={onSetKeepSandbox}
      onKillSandbox={onKillSandbox}
      onReassignSandbox={onReassignSandbox}
      onEditText={onEditText}
      onDismissCard={dismissCard}
      onDismissAll={dismissAll}
      onOpenTickets={refreshBoard}
      ticketsRefreshing={refreshing}
      lastSeenId={lastSeenId}
      hasMoreHistory={hasMoreHistory}
      loadingMoreHistory={loadingMoreHistory}
      onLoadMoreHistory={loadMoreHistory}
      onRefreshFeed={refreshFeed}
      notificationMode={notificationMode}
      onSelectNotificationMode={setNotificationMode}
      pushStatus={pushStatus}
      onEnablePush={() => {
        void enablePush();
      }}
      onDisablePush={() => {
        void disablePush();
      }}
    />
  );
}

export function PrimaryScreen(): JSX.Element {
  return (
    <BoardProvider>
      <FeedProvider>
        <ActivityProvider>
          <VoiceProvider>
            <PrimaryScreenBody />
          </VoiceProvider>
        </ActivityProvider>
      </FeedProvider>
    </BoardProvider>
  );
}
