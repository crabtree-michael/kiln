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
import { acceptTicket } from '@/transport/transport';
import { PrimaryScreenView } from '@/components/PrimaryScreenView';

function PrimaryScreenBody(): JSX.Element {
  const { feed, connectionState } = useFeedStore();
  const { board, refreshBoard, refreshing } = useBoardStore();
  const { thinking, toasts, dismiss } = useActivityStore();

  const onAccept = useCallback((ticketId: string): void => {
    // Tap-accept routes straight to the accept endpoint (08 §5 / D6); the
    // resulting board + feed transitions come back over the stream.
    void acceptTicket(ticketId);
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
      onOpenStreams={refreshBoard}
      streamsRefreshing={refreshing}
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
