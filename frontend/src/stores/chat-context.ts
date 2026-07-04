// Split from chat-store.tsx so that file exports only the `ChatProvider`
// component (react-refresh/only-export-components) — this file carries the
// message shape, the context, and the consumer hook.
import { createContext, useContext } from 'react';

export type ChatMessageRole = 'user' | 'kiln';

/**
 * `sent`: reconciled with a server `message_id` (either fetched history or a
 * `say` event, or a user send whose POST resolved).
 * `pending`: an optimistic user send awaiting the `POST /api/message` response.
 * `failed`: the `POST` rejected; the bubble carries a retry affordance (07 §8).
 */
export type ChatMessageStatus = 'sent' | 'pending' | 'failed';

export interface ChatMessage {
  /** Stable React list key; for optimistic sends this predates a server `message_id`. */
  clientId: string;
  /** `null` until reconciled with the server (07 §5's "reconciled by message_id"). */
  messageId: number | null;
  role: ChatMessageRole;
  text: string;
  /** ISO timestamp; provisional (client-generated) for a still-pending send. */
  timestamp: string;
  status: ChatMessageStatus;
}

export interface ChatStoreValue {
  /** Oldest-first, matching `GET /api/messages` order (07 §4). */
  messages: ChatMessage[];
  /** Optimistically appends a user message, then reconciles/marks it failed. */
  sendMessage: (text: string) => Promise<void>;
  /** Re-attempts a `failed` message by its `clientId`. */
  retryMessage: (clientId: string) => Promise<void>;
}

export const ChatStoreContext = createContext<ChatStoreValue | undefined>(undefined);

export function useChatStore(): ChatStoreValue {
  const context = useContext(ChatStoreContext);
  if (context === undefined) {
    throw new Error('useChatStore must be used within a ChatProvider');
  }
  return context;
}
