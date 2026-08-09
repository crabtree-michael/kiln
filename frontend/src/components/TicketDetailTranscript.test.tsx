// TicketDetailTranscript (08 §5, 09 §4): the live voice transcript rendered in the
// ticket sheet's dock. Like the Dock tests, it is a presentational consumer of the
// voice store, so `useVoice` is mocked per case — no mic/socket I/O. Covers the
// self-gate (nothing until there is text), the settled/ghosted-tail spans, and the
// listening-only caret. Plus the keyboard half: in typed input this same panel IS
// the field, so the sheet has one place where the message being composed appears.
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { createRef } from 'react';
import { TicketDetailTranscript } from '@/components/TicketDetailTranscript';
import type { TicketKeyboard } from '@/components/use-ticket-keyboard';
import type { VoiceStoreValue } from '@/voice/voice-context';

let mockVoiceValue: VoiceStoreValue;
let mockKeyboard: TicketKeyboard;

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

function stubKeyboard(overrides: Partial<TicketKeyboard>): TicketKeyboard {
  return {
    open: false,
    draft: '',
    inputRef: createRef<HTMLTextAreaElement>(),
    setDraft: vi.fn(),
    begin: vi.fn(),
    end: vi.fn(),
    submit: vi.fn(),
    ...overrides,
  };
}

describe('TicketDetailTranscript', () => {
  beforeEach(() => {
    mockKeyboard = stubKeyboard({});
  });

  it('renders nothing when there is no transcript text (visible only while speaking)', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: '', tailText: '' });
    const { container } = render(<TicketDetailTranscript keyboard={mockKeyboard} />);
    expect(container.querySelector('[data-role="ticket-detail-transcript"]')).toBeNull();
  });

  it('shows the settled words in ink and the still-forming tail ghosted', () => {
    mockVoiceValue = stubVoice({ settledText: 'move the button', tailText: ' to the top' });
    render(<TicketDetailTranscript keyboard={mockKeyboard} />);

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
    const { container, rerender } = render(<TicketDetailTranscript keyboard={mockKeyboard} />);
    expect(container.querySelector('[data-role="ticket-detail-caret"]')).not.toBeNull();

    mockVoiceValue = stubVoice({ micState: 'paused', settledText: 'hello' });
    rerender(<TicketDetailTranscript keyboard={mockKeyboard} />);
    expect(container.querySelector('[data-role="ticket-detail-caret"]')).toBeNull();
  });

  it('tapping the words hands them over for correction, like the dock’s (09 §4a)', () => {
    const beginEdit = vi.fn();
    mockVoiceValue = stubVoice({ settledText: 'move the buton', beginEdit });
    render(<TicketDetailTranscript keyboard={mockKeyboard} />);

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
    const { container } = render(<TicketDetailTranscript keyboard={mockKeyboard} />);

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
    const { rerender } = render(<TicketDetailTranscript keyboard={mockKeyboard} />);
    fireEvent.click(screen.getByRole('button', { name: /^Edit transcript:/ }));
    mockVoiceValue = stubVoice({
      micState: 'paused',
      settledText: 'move the buton',
      editing: true,
    });
    rerender(<TicketDetailTranscript keyboard={mockKeyboard} />);
    expect(document.activeElement).toBe(screen.getByLabelText('Edit transcript'));

    cleanup();

    // An edit begun elsewhere (the dock's own words) renders the field here too,
    // but must not pull focus out of the surface the user is actually in.
    mockVoiceValue = stubVoice({
      micState: 'paused',
      settledText: 'move the buton',
      editing: true,
    });
    render(<TicketDetailTranscript keyboard={mockKeyboard} />);
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
    const { unmount } = render(<TicketDetailTranscript keyboard={mockKeyboard} />);
    unmount();
    expect(endEdit).toHaveBeenCalled();
  });

  it('stays on screen through an edit that empties the text', () => {
    mockVoiceValue = stubVoice({ micState: 'paused', settledText: '', editing: true });
    const { container } = render(<TicketDetailTranscript keyboard={mockKeyboard} />);
    expect(container.querySelector('[data-role="ticket-detail-transcript"]')).not.toBeNull();
  });

  it('reflects the mic state on the transcript container', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', tailText: 'typing…' });
    const { container } = render(<TicketDetailTranscript keyboard={mockKeyboard} />);
    expect(container.querySelector('[data-role="ticket-detail-transcript"]')).toHaveAttribute(
      'data-dock-state',
      'listening',
    );
  });

  it('becomes the typed field in keyboard mode — same panel, not a second one', () => {
    mockVoiceValue = stubVoice({ micState: 'paused' });
    mockKeyboard = stubKeyboard({ open: true, draft: 'move the button' });
    const { container } = render(<TicketDetailTranscript keyboard={mockKeyboard} />);

    const panel = container.querySelector('[data-role="ticket-detail-transcript"]');
    expect(panel).not.toBeNull();
    expect(panel).toHaveAttribute('data-dock-state', 'keyboard');
    const field = screen.getByRole('textbox', { name: 'Message' });
    expect(field).toHaveValue('move the button');
    expect(field).toHaveAttribute('data-role', 'ticket-detail-input');
  });

  it('takes the caret the moment typed input opens, so the toggle is the only tap', () => {
    mockVoiceValue = stubVoice({ micState: 'paused' });
    mockKeyboard = stubKeyboard({ open: true });
    render(<TicketDetailTranscript keyboard={mockKeyboard} />);
    expect(screen.getByRole('textbox', { name: 'Message' })).toHaveFocus();
  });

  it('writes every keystroke to the shared draft, and sends on Enter', () => {
    const setDraft = vi.fn();
    const submit = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'paused' });
    mockKeyboard = stubKeyboard({ open: true, draft: 'move the', setDraft, submit });
    render(<TicketDetailTranscript keyboard={mockKeyboard} />);

    const field = screen.getByRole('textbox', { name: 'Message' });
    fireEvent.change(field, { target: { value: 'move the button' } });
    expect(setDraft).toHaveBeenLastCalledWith('move the button');

    fireEvent.keyDown(field, { key: 'Enter' });
    expect(submit).toHaveBeenCalledTimes(1);
  });

  it('Shift+Enter writes a newline instead of sending', () => {
    const submit = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'paused' });
    mockKeyboard = stubKeyboard({ open: true, draft: 'first line', submit });
    render(<TicketDetailTranscript keyboard={mockKeyboard} />);
    fireEvent.keyDown(screen.getByRole('textbox', { name: 'Message' }), {
      key: 'Enter',
      shiftKey: true,
    });
    expect(submit).not.toHaveBeenCalled();
  });

  it('shows the typed field even with no transcript — the voice self-gate does not apply', () => {
    // `begin` stopped the mic and dropped what it had heard, so an empty
    // transcript is the NORMAL reading here; gating the panel on transcript text
    // would leave the user typing into nothing.
    mockVoiceValue = stubVoice({ micState: 'paused', settledText: '', tailText: '' });
    mockKeyboard = stubKeyboard({ open: true, draft: '' });
    const { container } = render(<TicketDetailTranscript keyboard={mockKeyboard} />);
    expect(container.querySelector('[data-role="ticket-detail-transcript"]')).not.toBeNull();
    expect(container.querySelector('[data-role="ticket-detail-settled"]')).toBeNull();
  });
});
