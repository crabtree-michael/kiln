// ChatPanel image-snapshot target (07 §9): user/kiln/pending/failed bubbles
// rendered distinctly, with an inline retry affordance on failed sends —
// never a modal (07 §7–§8).
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { ChatPanel } from '@/components/ChatPanel';
import type { ChatMessage } from '@/stores/chat-context';

function message(
  overrides: Partial<ChatMessage> & Pick<ChatMessage, 'clientId' | 'role' | 'text' | 'status'>,
): ChatMessage {
  return {
    messageId: null,
    timestamp: '2026-07-01T00:00:00Z',
    ...overrides,
  };
}

describe('ChatPanel', () => {
  it('renders user, kiln, pending, and failed messages with distinct data-status', () => {
    const messages: ChatMessage[] = [
      message({
        clientId: 'c1',
        role: 'user',
        text: 'a sent user message',
        status: 'sent',
        messageId: 1,
      }),
      message({ clientId: 'c2', role: 'kiln', text: 'a kiln reply', status: 'sent', messageId: 2 }),
      message({ clientId: 'c3', role: 'user', text: 'an optimistic send', status: 'pending' }),
      message({ clientId: 'c4', role: 'user', text: 'a failed send', status: 'failed' }),
    ];

    render(<ChatPanel messages={messages} onSend={vi.fn()} onRetry={vi.fn()} />);

    const items = screen.getAllByRole('listitem');
    expect(items).toHaveLength(4);
    expect(items.map((item) => item.getAttribute('data-status'))).toEqual([
      'sent',
      'sent',
      'pending',
      'failed',
    ]);
  });

  it('shows a retry affordance only on the failed message, inline (never a modal) (07 §8)', () => {
    const messages: ChatMessage[] = [
      message({ clientId: 'c1', role: 'user', text: 'sent', status: 'sent', messageId: 1 }),
      message({ clientId: 'c2', role: 'user', text: 'failed', status: 'failed' }),
    ];

    render(<ChatPanel messages={messages} onSend={vi.fn()} onRetry={vi.fn()} />);

    expect(screen.getAllByRole('button', { name: 'Retry' })).toHaveLength(1);
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('calls onRetry with the failed message clientId when Retry is clicked', () => {
    const onRetry = vi.fn().mockResolvedValue(undefined);
    const messages: ChatMessage[] = [
      message({ clientId: 'c9', role: 'user', text: 'failed', status: 'failed' }),
    ];

    render(<ChatPanel messages={messages} onSend={vi.fn()} onRetry={onRetry} />);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));

    expect(onRetry).toHaveBeenCalledWith('c9');
  });

  it('calls onSend with trimmed, non-empty text and clears the input', () => {
    const onSend = vi.fn().mockResolvedValue(undefined);

    render(<ChatPanel messages={[]} onSend={onSend} onRetry={vi.fn()} />);

    const input = screen.getByLabelText('Message');
    fireEvent.change(input, { target: { value: '  build the widget  ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    expect(onSend).toHaveBeenCalledWith('build the widget');
    expect(input).toHaveValue('');
  });

  it('does not call onSend for whitespace-only input', () => {
    const onSend = vi.fn().mockResolvedValue(undefined);

    render(<ChatPanel messages={[]} onSend={onSend} onRetry={vi.fn()} />);

    const input = screen.getByLabelText('Message');
    fireEvent.change(input, { target: { value: '   ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    expect(onSend).not.toHaveBeenCalled();
  });

  it('matches the DOM-structure snapshot for user/kiln/pending/failed bubbles (07 §9 target)', () => {
    const messages: ChatMessage[] = [
      message({ clientId: 'c1', role: 'user', text: 'user message', status: 'sent', messageId: 1 }),
      message({ clientId: 'c2', role: 'kiln', text: 'kiln message', status: 'sent', messageId: 2 }),
      message({ clientId: 'c3', role: 'user', text: 'pending message', status: 'pending' }),
      message({ clientId: 'c4', role: 'user', text: 'failed message', status: 'failed' }),
    ];

    const { container } = render(
      <ChatPanel messages={messages} onSend={vi.fn()} onRetry={vi.fn()} />,
    );

    expect(container).toMatchSnapshot();
  });
});
