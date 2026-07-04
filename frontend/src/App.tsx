// One screen (07 §7): board on top (~60%), chat panel below. Board is
// read-only — all mutation flows through the brain via chat (D5).
// Voice/notifications are deferred (07 §2); this client holds no
// authoritative state, it only composes the two stores over the transport
// module. `App.css` is plain CSS (no framework, D4) keyed off the
// components' existing `data-*` attributes.
import type { JSX } from 'react';
import { BoardProvider } from '@/stores/board-store';
import { ChatProvider } from '@/stores/chat-store';
import { useChatStore } from '@/stores/chat-context';
import { Board } from '@/components/Board';
import { ChatPanel } from '@/components/ChatPanel';
import '@/App.css';

function ConnectedChatPanel(): JSX.Element {
  const { messages, sendMessage, retryMessage } = useChatStore();
  return <ChatPanel messages={messages} onSend={sendMessage} onRetry={retryMessage} />;
}

export function App(): JSX.Element {
  return (
    <BoardProvider>
      <ChatProvider>
        <main className="app-shell">
          <header className="app-header">
            <h1>Kiln</h1>
          </header>
          <div className="app-board-region">
            <Board />
          </div>
          <div className="app-chat-region">
            <ConnectedChatPanel />
          </div>
        </main>
      </ChatProvider>
    </BoardProvider>
  );
}
