// TicketDetailTranscript (08 §5, 09 §4): the live voice transcript rendered in the
// ticket sheet's dock. Like the Dock tests, it is a presentational consumer of the
// voice store, so `useVoice` is mocked per case — no mic/socket I/O. Covers the
// self-gate (nothing until there is text), the settled/ghosted-tail spans, and the
// listening-only caret.
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { TicketDetailTranscript } from '@/components/TicketDetailTranscript';
import type { VoiceStoreValue } from '@/voice/voice-context';

let mockVoiceValue: VoiceStoreValue;

vi.mock('@/voice/voice-context', () => ({
  useVoice: (): VoiceStoreValue => mockVoiceValue,
}));

function stubVoice(overrides: Partial<VoiceStoreValue>): VoiceStoreValue {
  return {
    micState: 'listening',
    connecting: false,
    settledText: '',
    tailText: '',
    pause: vi.fn(),
    resume: vi.fn(),
    cancel: vi.fn(),
    sendNow: vi.fn(),
    countingDown: false,
    sendImminent: false,
    delaySend: vi.fn(),
    getSendCountdown: vi.fn(() => null),
    getLevel: vi.fn(() => 0),
    keyboardMode: false,
    openKeyboard: vi.fn(),
    closeKeyboard: vi.fn(),
    submitText: vi.fn(() => Promise.resolve(true)),
    setTicketContext: vi.fn(),
    ...overrides,
  };
}

describe('TicketDetailTranscript', () => {
  it('renders nothing when there is no transcript text (visible only while speaking)', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: '', tailText: '' });
    const { container } = render(<TicketDetailTranscript />);
    expect(container.querySelector('[data-role="ticket-detail-transcript"]')).toBeNull();
  });

  it('shows the settled words in ink and the still-forming tail ghosted', () => {
    mockVoiceValue = stubVoice({ settledText: 'move the button', tailText: ' to the top' });
    render(<TicketDetailTranscript />);

    expect(screen.getByText('move the button')).toHaveAttribute(
      'data-role',
      'ticket-detail-settled',
    );
    const tail = screen.getByText('to the top');
    expect(tail).toHaveAttribute('data-role', 'ticket-detail-tail');
    expect(tail).toHaveAttribute('data-ghost', 'true');
  });

  it('shows the caret while listening, hides it when paused', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: 'hello' });
    const { container, rerender } = render(<TicketDetailTranscript />);
    expect(container.querySelector('[data-role="ticket-detail-caret"]')).not.toBeNull();

    mockVoiceValue = stubVoice({ micState: 'paused', settledText: 'hello' });
    rerender(<TicketDetailTranscript />);
    expect(container.querySelector('[data-role="ticket-detail-caret"]')).toBeNull();
  });

  it('reflects the mic state on the transcript container', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', tailText: 'typing…' });
    const { container } = render(<TicketDetailTranscript />);
    expect(container.querySelector('[data-role="ticket-detail-transcript"]')).toHaveAttribute(
      'data-dock-state',
      'listening',
    );
  });
});
