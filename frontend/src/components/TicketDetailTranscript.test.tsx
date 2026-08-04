// TicketDetailTranscript (08 §5, 09 §4): the live voice transcript rendered in the
// ticket sheet's dock. Like the Dock tests, it is a presentational consumer of the
// voice store, so `useVoice` is mocked per case — no mic/socket I/O. Covers the
// self-gate (nothing until there is text), the settled/ghosted-tail spans, and the
// listening-only caret.
import { describe, it, expect, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
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
    editing: false,
    beginEdit: vi.fn(),
    editTranscript: vi.fn(),
    endEdit: vi.fn(),
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

  it('tapping the words hands them over for correction, like the dock’s (09 §4a)', () => {
    const beginEdit = vi.fn();
    mockVoiceValue = stubVoice({ settledText: 'move the buton', beginEdit });
    render(<TicketDetailTranscript />);

    fireEvent.click(screen.getByRole('button', { name: /^Edit transcript:/ }));
    expect(beginEdit).toHaveBeenCalled();
  });

  it('renders the field over the same words while editing, and ends the edit on blur', () => {
    const editTranscript = vi.fn();
    const endEdit = vi.fn();
    mockVoiceValue = stubVoice({
      micState: 'paused',
      settledText: 'move the buton',
      editing: true,
      editTranscript,
      endEdit,
    });
    const { container } = render(<TicketDetailTranscript />);

    const field = screen.getByLabelText('Edit transcript');
    expect(field).toHaveValue('move the buton');
    expect(container.querySelector('[data-role="ticket-detail-settled"]')).toBeNull();

    fireEvent.change(field, { target: { value: 'move the button' } });
    expect(editTranscript).toHaveBeenCalledWith('move the button');
    fireEvent.blur(field);
    expect(endEdit).toHaveBeenCalled();
  });

  it('takes the caret when the tap landed on this line, and not when it didn’t', () => {
    // The main dock is a second view of the same buffer and stays mounted behind
    // the sheet, so both surfaces swap in a field when `editing` flips. Only the
    // one that was tapped may focus, or the two fight over the caret.
    mockVoiceValue = stubVoice({ settledText: 'move the buton' });
    const { rerender } = render(<TicketDetailTranscript />);
    fireEvent.click(screen.getByRole('button', { name: /^Edit transcript:/ }));
    mockVoiceValue = stubVoice({
      micState: 'paused',
      settledText: 'move the buton',
      editing: true,
    });
    rerender(<TicketDetailTranscript />);
    expect(document.activeElement).toBe(screen.getByLabelText('Edit transcript'));

    cleanup();

    // An edit begun elsewhere (the dock's own words) renders the field here too,
    // but must not pull focus out of the surface the user is actually in.
    mockVoiceValue = stubVoice({
      micState: 'paused',
      settledText: 'move the buton',
      editing: true,
    });
    render(<TicketDetailTranscript />);
    expect(document.activeElement).not.toBe(screen.getByLabelText('Edit transcript'));
  });

  it('ends the edit when the sheet closes mid-correction', () => {
    // Removing a focused element fires no blur, so without this the store would
    // sit `editing` forever with the auto-send frozen and nobody in the field.
    const endEdit = vi.fn();
    mockVoiceValue = stubVoice({
      micState: 'paused',
      settledText: 'move the buton',
      editing: true,
      endEdit,
    });
    const { unmount } = render(<TicketDetailTranscript />);
    unmount();
    expect(endEdit).toHaveBeenCalled();
  });

  it('stays on screen through an edit that empties the text', () => {
    mockVoiceValue = stubVoice({ micState: 'paused', settledText: '', editing: true });
    const { container } = render(<TicketDetailTranscript />);
    expect(container.querySelector('[data-role="ticket-detail-transcript"]')).not.toBeNull();
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
