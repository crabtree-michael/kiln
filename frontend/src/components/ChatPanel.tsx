// Image-snapshot target (07 §9): user/kiln/pending/failed message bubbles.
// Sending is optimistic; a failed POST marks the bubble with a retry
// affordance, inline — never a modal (07 §7–§8). Render is a placeholder;
// the solution phase supplies the real bubble styling.
import { useState, type ChangeEvent, type FormEvent, type JSX } from 'react';
import type { ChatMessage } from '@/stores/chat-context';

export interface ChatPanelProps {
  messages: ChatMessage[];
  onSend: (text: string) => Promise<void>;
  onRetry: (clientId: string) => Promise<void>;
}

export function ChatPanel({ messages, onSend, onRetry }: ChatPanelProps): JSX.Element {
  const [draft, setDraft] = useState('');

  const handleChange = (event: ChangeEvent<HTMLInputElement>): void => {
    setDraft(event.target.value);
  };

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const text = draft.trim();
    if (text === '') {
      return;
    }
    setDraft('');
    void onSend(text);
  };

  return (
    <section aria-label="Chat" data-role="chat-panel">
      <ul data-role="chat-transcript">
        {messages.map((message) => (
          <li
            key={message.clientId}
            data-role="chat-message"
            data-status={message.status}
            data-message-role={message.role}
          >
            <span data-role="chat-message-role">{message.role}</span>
            <span data-role="chat-message-text">{message.text}</span>
            {message.status === 'failed' && (
              <button type="button" onClick={() => void onRetry(message.clientId)}>
                Retry
              </button>
            )}
          </li>
        ))}
      </ul>
      <form onSubmit={handleSubmit}>
        <input aria-label="Message" value={draft} onChange={handleChange} />
        <button type="submit">Send</button>
      </form>
    </section>
  );
}
