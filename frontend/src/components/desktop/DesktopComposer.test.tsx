// The desktop input (13 §7, D5a): a big mic on the left and ONE field to its
// right that is both the live transcript and the typed draft. Like the dock's
// tests, the voice store is mocked to a fixed value per case — deterministic, and
// no mic/socket I/O.
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { DesktopComposer } from '@/components/desktop/DesktopComposer';
import type { VoiceStoreValue } from '@/voice/voice-context';

let mockVoiceValue: VoiceStoreValue;

vi.mock('@/voice/voice-context', () => ({
  useVoice: (): VoiceStoreValue => mockVoiceValue,
}));

function stubVoice(overrides: Partial<VoiceStoreValue> = {}): VoiceStoreValue {
  return {
    // The resting mic state is `paused` — there is no `idle` in the voice
    // machine's union (`listening | paused | denied | retry`).
    micState: 'paused',
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

function input(): HTMLTextAreaElement {
  return screen.getByLabelText('Say something');
}

describe('DesktopComposer', () => {
  beforeEach(() => {
    mockVoiceValue = stubVoice();
  });

  it('leads with the mic — voice is the primary input at a desk too (13 D5a)', () => {
    const { container } = render(<DesktopComposer />);
    const composer = container.querySelector('[data-role="desktop-composer"]');
    const mic = screen.getByRole('button', { name: 'Talk' });
    expect(mic).toBeInTheDocument();
    // First in the row, and to the left of the field: the ordering IS the
    // weighting, and a mic after the field would read as an affordance on it.
    expect(composer?.firstElementChild).toBe(mic);
    expect(
      mic.compareDocumentPosition(container.querySelector('[data-role="desktop-field"]')!) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it('offers exactly one text surface — the transcript is not a second field', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: 'add a retry' });
    const { container } = render(<DesktopComposer />);
    expect(container.querySelectorAll('textarea')).toHaveLength(1);
    // The heard words live INSIDE the field, not in a block stacked above it.
    const field = container.querySelector('[data-role="desktop-field"]');
    expect(field?.querySelector('[data-role="desktop-heard"]')).not.toBeNull();
    // And it is one line, not a form: no title field, no priority select (13 §7).
    expect(screen.queryByRole('form')).toBeNull();
    expect(screen.queryByLabelText(/title/i)).toBeNull();
  });

  it('shows the live transcript in the field, settled ink then ghosted tail', () => {
    mockVoiceValue = stubVoice({
      micState: 'listening',
      settledText: 'add a retry to',
      tailText: 'the poller',
    });
    const { container } = render(<DesktopComposer />);
    const heard = container.querySelector('[data-role="desktop-heard"]');
    expect(heard?.textContent).toContain('add a retry to the poller');
    expect(heard?.querySelector('[data-role="dock-tail"]')?.textContent).toContain('the poller');
  });

  it('renders no heard block at rest', () => {
    const { container } = render(<DesktopComposer />);
    expect(container.querySelector('[data-role="desktop-heard"]')).toBeNull();
    expect(container.querySelector('[data-role="desktop-field"]')).not.toHaveAttribute(
      'data-hearing',
    );
  });

  it('Enter sends the draft through the same seam a spoken utterance uses', async () => {
    const submitText = vi.fn(() => Promise.resolve(true));
    mockVoiceValue = stubVoice({ submitText });
    render(<DesktopComposer />);

    fireEvent.change(input(), { target: { value: 'ship the rail' } });
    fireEvent.keyDown(input(), { key: 'Enter' });

    expect(submitText).toHaveBeenCalledWith('ship the rail');
    await waitFor(() => {
      expect(input()).toHaveValue('');
    });
  });

  it('Shift+Enter writes a newline instead of sending', () => {
    const submitText = vi.fn(() => Promise.resolve(true));
    mockVoiceValue = stubVoice({ submitText });
    render(<DesktopComposer />);

    fireEvent.change(input(), { target: { value: 'line one' } });
    fireEvent.keyDown(input(), { key: 'Enter', shiftKey: true });
    expect(submitText).not.toHaveBeenCalled();
  });

  it('keeps the text when the send fails, so a sentence is never silently lost', async () => {
    const submitText = vi.fn(() => Promise.resolve(false));
    mockVoiceValue = stubVoice({ submitText });
    render(<DesktopComposer />);

    fireEvent.change(input(), { target: { value: 'ship the rail' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => {
      expect(submitText).toHaveBeenCalled();
    });
    expect(input()).toHaveValue('ship the rail');
  });

  it('will not send an empty or whitespace-only field', () => {
    const submitText = vi.fn(() => Promise.resolve(true));
    mockVoiceValue = stubVoice({ submitText });
    render(<DesktopComposer />);

    expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
    fireEvent.change(input(), { target: { value: '   ' } });
    fireEvent.keyDown(input(), { key: 'Enter' });
    expect(submitText).not.toHaveBeenCalled();
  });

  it('the one send button commits a spoken utterance through the store, not as typed text', () => {
    const sendNow = vi.fn();
    const submitText = vi.fn(() => Promise.resolve(true));
    mockVoiceValue = stubVoice({
      micState: 'listening',
      settledText: 'add a retry to ',
      tailText: 'the poller',
      sendNow,
      submitText,
    });
    render(<DesktopComposer />);

    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    expect(sendNow).toHaveBeenCalled();
    expect(submitText).not.toHaveBeenCalled();
  });

  it('the one clear button empties the field, whichever way the words got there', () => {
    const cancel = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: 'never mind', cancel });
    render(<DesktopComposer />);

    fireEvent.click(screen.getByRole('button', { name: 'Clear' }));
    expect(cancel).toHaveBeenCalled();
  });

  it('offers no clear button when there is nothing in the field to clear', () => {
    render(<DesktopComposer />);
    expect(screen.queryByRole('button', { name: 'Clear' })).toBeNull();
  });

  it('keeps the mic tappable while a transcript is up, unlike the sheet’s send-aware mic', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: 'something' });
    render(<DesktopComposer />);
    expect(screen.getByRole('button', { name: 'Talk' })).toBeInTheDocument();
  });

  describe('the handover — putting a cursor in the field takes the words over', () => {
    it('moves what was heard into the field as editable text', () => {
      const pause = vi.fn();
      const cancel = vi.fn();
      mockVoiceValue = stubVoice({
        micState: 'listening',
        settledText: 'add a retry to',
        tailText: 'the poler',
        pause,
        cancel,
      });
      render(<DesktopComposer />);

      fireEvent.focus(input());

      // The mic stops (which also disarms the end-of-turn auto-send, 09 §4) and
      // the store's copy is dropped, so the same sentence can't be both in the
      // field and in a pending commit.
      expect(pause).toHaveBeenCalled();
      expect(cancel).toHaveBeenCalled();
      expect(input()).toHaveValue('add a retry to the poler');
    });

    it('appends to anything already typed rather than replacing it', () => {
      mockVoiceValue = stubVoice({ micState: 'listening', settledText: 'to the poller' });
      render(<DesktopComposer />);

      fireEvent.change(input(), { target: { value: 'add a retry' } });
      fireEvent.focus(input());
      expect(input()).toHaveValue('add a retry to the poller');
    });

    it('then sends the corrected sentence as typed text', async () => {
      const submitText = vi.fn(() => Promise.resolve(true));
      const sendNow = vi.fn();
      mockVoiceValue = stubVoice({
        micState: 'listening',
        settledText: 'add a retry',
        submitText,
        sendNow,
      });
      render(<DesktopComposer />);

      fireEvent.focus(input());
      // The store has handed the words over, so the component now holds them.
      mockVoiceValue = stubVoice({ settledText: '', submitText, sendNow });
      fireEvent.change(input(), { target: { value: 'add a retry to the poller' } });
      fireEvent.keyDown(input(), { key: 'Enter' });

      expect(submitText).toHaveBeenCalledWith('add a retry to the poller');
      expect(sendNow).not.toHaveBeenCalled();
      await waitFor(() => {
        expect(input()).toHaveValue('');
      });
    });

    it('does nothing when the field is focused with no words on screen', () => {
      const pause = vi.fn();
      const cancel = vi.fn();
      mockVoiceValue = stubVoice({ pause, cancel });
      render(<DesktopComposer />);

      fireEvent.focus(input());
      expect(pause).not.toHaveBeenCalled();
      expect(cancel).not.toHaveBeenCalled();
    });
  });

  it('sends a half-typed thought and its spoken finish as one message', async () => {
    const submitText = vi.fn(() => Promise.resolve(true));
    const cancel = vi.fn();
    mockVoiceValue = stubVoice({
      micState: 'listening',
      settledText: 'to the poller',
      submitText,
      cancel,
    });
    const { rerender } = render(<DesktopComposer />);

    // Typed first (without focusing, so the handover doesn't fire), then spoken.
    fireEvent.change(input(), { target: { value: 'add a retry' } });
    rerender(<DesktopComposer />);
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    expect(cancel).toHaveBeenCalled();
    expect(submitText).toHaveBeenCalledWith('add a retry to the poller');
    await waitFor(() => {
      expect(input()).toHaveValue('');
    });
  });

  it('"/" from anywhere jumps to the input (13 §9)', () => {
    render(<DesktopComposer />);
    expect(document.activeElement).not.toBe(input());
    fireEvent.keyDown(document, { key: '/' });
    expect(document.activeElement).toBe(input());
  });

  it('Cmd/Ctrl-K jumps to the input too, even from inside a text field', () => {
    render(<DesktopComposer />);
    input().blur();
    fireEvent.keyDown(input(), { key: 'k', metaKey: true });
    expect(document.activeElement).toBe(input());
  });

  it('"/" typed inside a text entry stays a slash — it never yanks focus mid-sentence', () => {
    render(<DesktopComposer />);
    const other = document.createElement('input');
    document.body.appendChild(other);
    other.focus();

    fireEvent.keyDown(other, { key: '/' });
    expect(document.activeElement).toBe(other);
    other.remove();
  });

  it('Escape gets you back out of the input, keeping the draft', () => {
    render(<DesktopComposer />);
    input().focus();
    fireEvent.change(input(), { target: { value: 'half a thought' } });

    fireEvent.keyDown(input(), { key: 'Escape' });
    expect(document.activeElement).not.toBe(input());
    expect(input()).toHaveValue('half a thought');
  });
});
