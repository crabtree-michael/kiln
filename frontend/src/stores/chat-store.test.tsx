// Chat store tests (07 §3–§5, §8): fetched history seeding, live `say`
// events, optimistic sends reconciled by `message_id`, and retry. Transport
// is mocked at the module boundary. Scaffold's `ChatProvider` seeds
// `messages: []` and both `sendMessage`/`retryMessage` throw
// `not implemented`, so every test here is red until the solution phase
// wires the transport calls and the reducer logic.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { JSX } from 'react';
import { ChatProvider } from '@/stores/chat-store';
import { useChatStore } from '@/stores/chat-context';
import * as transport from '@/transport/transport';
import type {
  Message,
  MessagePostResponse,
  StreamConnection,
  StreamHandlers,
} from '@/transport/transport';

vi.mock('@/transport/transport', () => ({
  fetchBoard: vi.fn(),
  fetchMessages: vi.fn(),
  postMessage: vi.fn(),
  openStream: vi.fn(),
}));

function Probe(): JSX.Element {
  const { messages, sendMessage, retryMessage } = useChatStore();
  return (
    <div>
      <ul data-testid="messages">
        {messages.map((message) => (
          <li
            key={message.clientId}
            data-role={message.role}
            data-status={message.status}
            data-message-id={message.messageId ?? 'none'}
          >
            {message.text}
          </li>
        ))}
      </ul>
      <button type="button" onClick={() => void sendMessage('hello kiln')}>
        send
      </button>
      <button
        type="button"
        onClick={() => {
          const failed = messages.find((message) => message.status === 'failed');
          if (failed !== undefined) {
            void retryMessage(failed.clientId);
          }
        }}
      >
        retry
      </button>
    </div>
  );
}

describe('ChatProvider', () => {
  let capturedHandlers: StreamHandlers | undefined;

  beforeEach(() => {
    capturedHandlers = undefined;
    vi.mocked(transport.fetchMessages).mockResolvedValue([]);
    vi.mocked(transport.openStream).mockImplementation((handlers): StreamConnection => {
      capturedHandlers = handlers;
      return { close: vi.fn() };
    });
  });

  afterEach(() => {
    vi.mocked(transport.fetchMessages).mockReset();
    vi.mocked(transport.postMessage).mockReset();
    vi.mocked(transport.openStream).mockReset();
  });

  it('seeds messages from GET /api/messages on mount, oldest-first (07 §5)', async () => {
    const history: Message[] = [
      { message_id: 1, role: 'user', text: 'first', timestamp: '2026-07-01T00:00:00Z' },
      { message_id: 2, role: 'kiln', text: 'second', timestamp: '2026-07-01T00:01:00Z' },
    ];
    vi.mocked(transport.fetchMessages).mockResolvedValue(history);

    render(
      <ChatProvider>
        <Probe />
      </ChatProvider>,
    );

    await waitFor(() => {
      const items = screen.getByTestId('messages').querySelectorAll('li');
      expect(items).toHaveLength(2);
    });
    const items = screen.getByTestId('messages').querySelectorAll('li');
    expect(items[0]?.textContent).toBe('first');
    expect(items[1]?.textContent).toBe('second');
    expect(items[0]?.getAttribute('data-status')).toBe('sent');
  });

  it('appends a `say` SSE event as a sent kiln message (07 §3)', async () => {
    render(
      <ChatProvider>
        <Probe />
      </ChatProvider>,
    );

    await waitFor(() => {
      expect(capturedHandlers).not.toBeUndefined();
    });

    act(() => {
      capturedHandlers?.onSay({
        message_id: 99,
        text: 'blocked on a decision',
        at: '2026-07-01T00:02:00Z',
      });
    });

    await waitFor(() => {
      const items = screen.getByTestId('messages').querySelectorAll('li');
      expect(items).toHaveLength(1);
    });
    const item = screen.getByTestId('messages').querySelector('li');
    expect(item?.textContent).toBe('blocked on a decision');
    expect(item?.getAttribute('data-role')).toBe('kiln');
    expect(item?.getAttribute('data-status')).toBe('sent');
    expect(item?.getAttribute('data-message-id')).toBe('99');
  });

  it('appends an optimistic pending message immediately, then reconciles to sent by message_id (07 §5, §9)', async () => {
    let resolvePost: ((value: MessagePostResponse) => void) | undefined;
    vi.mocked(transport.postMessage).mockImplementation(
      () =>
        new Promise<MessagePostResponse>((resolve) => {
          resolvePost = resolve;
        }),
    );

    render(
      <ChatProvider>
        <Probe />
      </ChatProvider>,
    );
    await waitFor(() => {
      expect(transport.fetchMessages).toHaveBeenCalledTimes(1);
    });

    fireEvent.click(screen.getByText('send'));

    await waitFor(() => {
      const items = screen.getByTestId('messages').querySelectorAll('li');
      expect(items).toHaveLength(1);
    });
    const pendingItem = screen.getByTestId('messages').querySelector('li');
    expect(pendingItem?.getAttribute('data-status')).toBe('pending');
    expect(pendingItem?.getAttribute('data-role')).toBe('user');
    expect(pendingItem?.textContent).toBe('hello kiln');

    await act(async () => {
      resolvePost?.({ event_id: 1, message_id: 55 });
      await Promise.resolve();
    });

    await waitFor(() => {
      const items = screen.getByTestId('messages').querySelectorAll('li');
      // Reconciled in place — not duplicated.
      expect(items).toHaveLength(1);
      expect(items[0]?.getAttribute('data-status')).toBe('sent');
      expect(items[0]?.getAttribute('data-message-id')).toBe('55');
    });
  });

  it('marks a send as failed when POST /api/message rejects, with a retry that resends (07 §8)', async () => {
    vi.mocked(transport.postMessage).mockRejectedValueOnce(new Error('network error'));

    render(
      <ChatProvider>
        <Probe />
      </ChatProvider>,
    );
    await waitFor(() => {
      expect(transport.fetchMessages).toHaveBeenCalledTimes(1);
    });

    fireEvent.click(screen.getByText('send'));

    await waitFor(() => {
      const item = screen.getByTestId('messages').querySelector('li');
      expect(item?.getAttribute('data-status')).toBe('failed');
    });

    vi.mocked(transport.postMessage).mockResolvedValueOnce({ event_id: 2, message_id: 77 });
    fireEvent.click(screen.getByText('retry'));

    await waitFor(() => {
      const items = screen.getByTestId('messages').querySelectorAll('li');
      expect(items).toHaveLength(1);
      expect(items[0]?.getAttribute('data-status')).toBe('sent');
      expect(items[0]?.getAttribute('data-message-id')).toBe('77');
    });
    expect(transport.postMessage).toHaveBeenCalledTimes(2);
    expect(vi.mocked(transport.postMessage).mock.calls[1]?.[0]).toBe('hello kiln');
  });
});
