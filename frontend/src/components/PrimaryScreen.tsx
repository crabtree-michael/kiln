// Primary screen (08 §6). Composes the feed + activity providers and bridges
// their stores into the presentational `PrimaryScreenView` — the same shape as
// `App` bridging its stores into `Board`/`ChatPanel`. All markup, CSS, and the
// 08 §F selector surface live in the presentational tree; this file is only the
// wiring seam (stores → props, Accept → transport).
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
import { acceptTicket, postMessage } from '@/transport/transport';
import { PrimaryScreenView } from '@/components/PrimaryScreenView';
import { useKeyboardViewport } from '@/components/use-keyboard-viewport';

function PrimaryScreenBody(): JSX.Element {
  // Keep the screen column matched to the visible viewport while the keyboard is
  // open, so the dock stays docked above it rather than sliding off-screen.
  useKeyboardViewport();

  const {
    feed,
    connectionState,
    lastSeenId,
    hasMoreHistory,
    loadingMoreHistory,
    loadMoreHistory,
    refreshFeed,
    acceptProposal,
    dismissCard,
    dismissAll,
  } = useFeedStore();
  const { board, refreshBoard, refreshing } = useBoardStore();
  const { thinking, toasts, dismiss } = useActivityStore();
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

  return (
    <PrimaryScreenView
      feed={feed}
      board={board}
      connectionState={connectionState}
      thinking={thinking}
      toasts={toasts}
      onDismiss={dismiss}
      onAccept={onAccept}
      onPoke={onPoke}
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
