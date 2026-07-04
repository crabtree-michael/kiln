// Primary screen (08 §6). Composes the feed + activity providers and bridges
// their stores into the presentational `PrimaryScreenView` — the same shape as
// `App` bridging its stores into `Board`/`ChatPanel`. All markup, CSS, and the
// 08 §F selector surface live in the presentational tree; this file is only the
// wiring seam (stores → props, Accept → transport).
import { useCallback, type JSX } from 'react';
import { FeedProvider } from '@/stores/feed-store';
import { ActivityProvider } from '@/stores/activity-store';
import { useFeedStore } from '@/stores/feed-context';
import { useActivityStore } from '@/stores/activity-context';
import { acceptTicket } from '@/transport/transport';
import { PrimaryScreenView } from '@/components/PrimaryScreenView';

function PrimaryScreenBody(): JSX.Element {
  const { feed, connectionState } = useFeedStore();
  const { thinking, pill, dismiss } = useActivityStore();

  const onAccept = useCallback((ticketId: string): void => {
    // Tap-accept routes straight to the accept endpoint (08 §5 / D6); the
    // resulting board + feed transitions come back over the stream.
    void acceptTicket(ticketId);
  }, []);

  return (
    <PrimaryScreenView
      feed={feed}
      connectionState={connectionState}
      thinking={thinking}
      pill={pill}
      onDismiss={dismiss}
      onAccept={onAccept}
    />
  );
}

export function PrimaryScreen(): JSX.Element {
  return (
    <FeedProvider>
      <ActivityProvider>
        <PrimaryScreenBody />
      </ActivityProvider>
    </FeedProvider>
  );
}
