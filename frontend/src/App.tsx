// Developer debug view (`/debug`): board on top, agent notifications in the
// middle, chat panel below. Board is read-only — all mutation flows through the
// brain via chat (D5). It holds no authoritative state; it only composes the
// stores over the transport module. The notifications panel reuses the feed
// store's notification plumbing (the brain-authored update/preview cards, 08 §3)
// rather than a second data path. `App.css` is plain CSS (no framework, D4)
// keyed off the components' existing `data-*` attributes.
import type { JSX } from 'react';
import { BoardProvider } from '@/stores/board-store';
import { ChatProvider } from '@/stores/chat-store';
import { FeedProvider } from '@/stores/feed-store';
import { useChatStore } from '@/stores/chat-context';
import { useFeedStore } from '@/stores/feed-context';
import { Board } from '@/components/Board';
import { ChatPanel } from '@/components/ChatPanel';
import { NotificationsPanel } from '@/components/NotificationsPanel';
import { ResetSessionButton } from '@/components/ResetSessionButton';
import type { FeedCard } from '@/transport/transport';
import '@/App.css';

function isNotification(card: FeedCard): boolean {
  return card.kind === 'update' || card.kind === 'preview';
}

function ConnectedChatPanel(): JSX.Element {
  const { messages, sendMessage, retryMessage } = useChatStore();
  return <ChatPanel messages={messages} onSend={sendMessage} onRetry={retryMessage} />;
}

function ConnectedNotificationsPanel(): JSX.Element {
  const { feed } = useFeedStore();
  const notifications = (feed?.cards ?? []).filter(isNotification);
  return <NotificationsPanel notifications={notifications} />;
}

export function App(): JSX.Element {
  return (
    <BoardProvider>
      <ChatProvider>
        <FeedProvider>
          <main className="app-shell">
            <header className="app-header">
              <h1>Kiln</h1>
              <ResetSessionButton />
            </header>
            <div className="app-board-region">
              <Board />
            </div>
            <div className="app-notifications-region">
              <ConnectedNotificationsPanel />
            </div>
            <div className="app-chat-region">
              <ConnectedChatPanel />
            </div>
          </main>
        </FeedProvider>
      </ChatProvider>
    </BoardProvider>
  );
}
