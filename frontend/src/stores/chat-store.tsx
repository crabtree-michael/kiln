// Chat store (07 §5): the fetched `GET /api/messages` page, `say` events
// appended live, and the user's own sends appended optimistically and
// reconciled by `message_id` once `POST /api/message` resolves. The
// transcript itself is server-owned (07 §3) — this store is a cache, not a
// source of truth. Live updates ride the single app-wide stream connection
// (`@/stores/stream-connection`), shared with the board store.
import { useCallback, useEffect, useMemo, useRef, useState, type JSX, type ReactNode } from 'react';
import { fetchMessages, postMessage } from '@/transport/transport';
import type { ConnectionState, Message, SayEvent } from '@/transport/transport';
import { ChatStoreContext, type ChatMessage, type ChatStoreValue } from '@/stores/chat-context';
import { subscribeStream } from '@/stores/stream-connection';

export interface ChatProviderProps {
  children: ReactNode;
}

let clientIdCounter = 0;

/** A stable client-side id for an optimistic send, predating any server `message_id`. */
function nextClientId(): string {
  clientIdCounter += 1;
  return `client-${String(clientIdCounter)}`;
}

function messageToChatMessage(message: Message): ChatMessage {
  return {
    clientId: `message-${String(message.message_id)}`,
    messageId: message.message_id,
    role: message.role,
    text: message.text,
    timestamp: message.timestamp,
    status: 'sent',
  };
}

function sayEventToChatMessage(event: SayEvent): ChatMessage {
  return {
    clientId: `message-${String(event.message_id)}`,
    messageId: event.message_id,
    role: 'kiln',
    text: event.text,
    timestamp: event.at,
    status: 'sent',
  };
}

export function ChatProvider({ children }: ChatProviderProps): JSX.Element {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const messagesRef = useRef<ChatMessage[]>(messages);

  // `sendMessage`/`retryMessage` need to read the *current* messages (to look
  // up a failed send's text) but must keep a stable identity across renders,
  // so state updates go through this helper to keep a ref in sync instead of
  // depending on `messages` directly.
  const updateMessages = useCallback((updater: (current: ChatMessage[]) => ChatMessage[]): void => {
    setMessages((current) => {
      const next = updater(current);
      messagesRef.current = next;
      return next;
    });
  }, []);

  const reconcileMessage = useCallback(
    (clientId: string, update: Partial<ChatMessage>): void => {
      updateMessages((current) =>
        current.map((message) =>
          message.clientId === clientId ? { ...message, ...update } : message,
        ),
      );
    },
    [updateMessages],
  );

  const postAndReconcile = useCallback(
    async (clientId: string, text: string): Promise<void> => {
      try {
        const response = await postMessage(text);
        reconcileMessage(clientId, { messageId: response.message_id, status: 'sent' });
      } catch {
        reconcileMessage(clientId, { status: 'failed' });
      }
    },
    [reconcileMessage],
  );

  // Seed the transcript from the persisted history on mount (07 §5).
  useEffect(() => {
    let cancelled = false;

    async function loadHistory(): Promise<void> {
      try {
        const history = await fetchMessages();
        if (!cancelled) {
          updateMessages(() => history.map(messageToChatMessage));
        }
      } catch {
        // Swallowed: an empty/stale transcript beats a crashed chat panel;
        // the next stream reopen retries the fetch (07 §8).
      }
    }

    void loadHistory();
    return () => {
      cancelled = true;
    };
  }, [updateMessages]);

  // Live `say` events, plus the reconnect-refetch contract (07 §5, §8): "on
  // every stream (re)open the client refetches /api/messages once to fill
  // any gap." Detected as a reconnecting -> connected transition so the
  // initial connect (already covered by the mount fetch above) doesn't
  // double-fetch, and a redundant `connected` with no intervening drop
  // doesn't either.
  useEffect(() => {
    let previousState: ConnectionState = 'connecting';

    async function refetchHistory(): Promise<void> {
      try {
        const history = await fetchMessages();
        const serverMessages = history.map(messageToChatMessage);
        updateMessages((current) => {
          const localOnly = current.filter((message) => message.status !== 'sent');
          return [...serverMessages, ...localOnly];
        });
      } catch {
        // Leave the existing (stale-but-visible) transcript in place.
      }
    }

    return subscribeStream({
      onBoard: () => {
        // The chat store doesn't care about board snapshots.
      },
      onSay: (event) => {
        updateMessages((current) => [...current, sayEventToChatMessage(event)]);
      },
      onConnectionStateChange: (state) => {
        if (state === 'connected' && previousState === 'reconnecting') {
          void refetchHistory();
        }
        previousState = state;
      },
    });
  }, [updateMessages]);

  const sendMessage = useCallback(
    async (text: string): Promise<void> => {
      const clientId = nextClientId();
      const optimistic: ChatMessage = {
        clientId,
        messageId: null,
        role: 'user',
        text,
        timestamp: new Date().toISOString(),
        status: 'pending',
      };
      updateMessages((current) => [...current, optimistic]);
      await postAndReconcile(clientId, text);
    },
    [postAndReconcile, updateMessages],
  );

  const retryMessage = useCallback(
    async (clientId: string): Promise<void> => {
      const target = messagesRef.current.find((message) => message.clientId === clientId);
      if (target === undefined) {
        return;
      }
      reconcileMessage(clientId, { status: 'pending' });
      await postAndReconcile(clientId, target.text);
    },
    [postAndReconcile, reconcileMessage],
  );

  const value = useMemo<ChatStoreValue>(
    () => ({ messages, sendMessage, retryMessage }),
    [messages, sendMessage, retryMessage],
  );

  return <ChatStoreContext.Provider value={value}>{children}</ChatStoreContext.Provider>;
}
