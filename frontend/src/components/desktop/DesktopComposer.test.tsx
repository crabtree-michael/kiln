// The desktop input (13 §7, D5a): a big mic on the left and ONE field to its
// right that is both the live transcript and the typed draft. Like the dock's
// tests, the voice store is mocked to a fixed value per case — deterministic, and
// no mic/socket I/O.
//
// The composer holds NO text state of its own (09 §4a): the field renders the
// store's transcript and writes every keystroke back to it, so these tests assert
// what it asks the store to do rather than what it kept. The store's side of that
// bargain — the mic release, and the auto-send that freezes while the field has
// focus and resumes on blur — is pinned in `voice-store.test.tsx`.
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
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

  it('the field IS the transcript — a keystroke rewrites it in the store, not a local draft', () => {
    const editTranscript = vi.fn();
    mockVoiceValue = stubVoice({ editTranscript });
    render(<DesktopComposer />);

    fireEvent.change(input(), { target: { value: 'ship the rail' } });
    expect(editTranscript).toHaveBeenCalledWith('ship the rail');
  });

  it('renders whatever the store holds, however the words got there', () => {
    // Typed words are transcript text like any other, so they come back through
    // the same prop the spoken ones do — there is nowhere else for them to live.
    mockVoiceValue = stubVoice({ settledText: 'ship the rail' });
    render(<DesktopComposer />);
    expect(input()).toHaveValue('ship the rail');
  });

  it('Enter sends the field through the store’s one send seam', () => {
    const sendNow = vi.fn();
    const submitText = vi.fn(() => Promise.resolve(true));
    mockVoiceValue = stubVoice({ settledText: 'ship the rail', sendNow, submitText });
    render(<DesktopComposer />);

    fireEvent.keyDown(input(), { key: 'Enter' });

    // One path for typed and spoken alike: the commit machine POSTs the displayed
    // transcript (09 §4). There is no separate typed-text submit anymore.
    expect(sendNow).toHaveBeenCalled();
    expect(submitText).not.toHaveBeenCalled();
  });

  it('Shift+Enter writes a newline instead of sending', () => {
    const sendNow = vi.fn();
    mockVoiceValue = stubVoice({ settledText: 'line one', sendNow });
    render(<DesktopComposer />);

    fireEvent.keyDown(input(), { key: 'Enter', shiftKey: true });
    expect(sendNow).not.toHaveBeenCalled();
  });

  it('never clears the field itself — a failed send leaves the sentence where it is', () => {
    // The store keeps the words on a failed POST (`commitFailed`, 09 §4) and clears
    // them on a successful one. The composer holding no copy is what makes that the
    // only rule there is: nothing here can lose a sentence the store still has.
    const sendNow = vi.fn();
    mockVoiceValue = stubVoice({ settledText: 'ship the rail', sendNow });
    render(<DesktopComposer />);

    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    expect(sendNow).toHaveBeenCalled();
    expect(input()).toHaveValue('ship the rail');
  });

  it('will not send an empty or whitespace-only field', () => {
    const sendNow = vi.fn();
    mockVoiceValue = stubVoice({ sendNow });
    const { rerender } = render(<DesktopComposer />);
    expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();

    mockVoiceValue = stubVoice({ settledText: '   ', sendNow });
    rerender(<DesktopComposer />);
    expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
    fireEvent.keyDown(input(), { key: 'Enter' });
    expect(sendNow).not.toHaveBeenCalled();
  });

  it('the one send button commits the spoken utterance through the store', () => {
    const sendNow = vi.fn();
    mockVoiceValue = stubVoice({
      micState: 'listening',
      settledText: 'add a retry to ',
      tailText: 'the poller',
      sendNow,
    });
    render(<DesktopComposer />);

    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    expect(sendNow).toHaveBeenCalled();
  });

  it('sends a half-typed thought and its spoken finish as one message', () => {
    // Both halves are already the same buffer — the typed words settled into the
    // transcript, the spoken finish is its tail — so "as one message" is just the
    // ordinary send. Nothing has to be merged at the last moment.
    const sendNow = vi.fn();
    mockVoiceValue = stubVoice({
      micState: 'listening',
      settledText: 'add a retry',
      tailText: 'to the poller',
      sendNow,
    });
    render(<DesktopComposer />);

    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    expect(sendNow).toHaveBeenCalled();
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
    it('hands the transcript over on focus and back on blur', () => {
      const beginEdit = vi.fn();
      const endEdit = vi.fn();
      mockVoiceValue = stubVoice({
        micState: 'listening',
        settledText: 'add a retry to',
        tailText: 'the poler',
        beginEdit,
        endEdit,
      });
      render(<DesktopComposer />);

      // Focus is the handover: the store stops the mic, folds the tail into the ink
      // and FREEZES the armed auto-send — it is not cancelled, so clicking away
      // resumes it on the corrected words (09 §4a).
      fireEvent.focus(input());
      expect(beginEdit).toHaveBeenCalled();
      expect(endEdit).not.toHaveBeenCalled();

      fireEvent.blur(input());
      expect(endEdit).toHaveBeenCalled();
    });

    it('shows the words as plain editable text once the handover lands', () => {
      // Mid-edit the field is no longer "hearing": the two-tone heard block gives
      // way to the textarea over the same buffer, so the words are never on screen
      // twice and what you edit is what would send.
      mockVoiceValue = stubVoice({
        micState: 'paused',
        settledText: 'add a retry to the poler',
        editing: true,
      });
      const { container } = render(<DesktopComposer />);

      expect(container.querySelector('[data-role="desktop-heard"]')).toBeNull();
      expect(container.querySelector('[data-role="desktop-field"]')).not.toHaveAttribute(
        'data-hearing',
      );
      expect(input()).toHaveValue('add a retry to the poler');
    });

    it('then sends the corrected sentence, not the heard one', () => {
      const editTranscript = vi.fn();
      const sendNow = vi.fn();
      mockVoiceValue = stubVoice({
        settledText: 'add a retry',
        editing: true,
        editTranscript,
        sendNow,
      });
      const { rerender } = render(<DesktopComposer />);

      fireEvent.change(input(), { target: { value: 'add a retry to the poller' } });
      expect(editTranscript).toHaveBeenCalledWith('add a retry to the poller');

      // The store now holds the correction, so the send fires that text.
      mockVoiceValue = stubVoice({
        settledText: 'add a retry to the poller',
        editing: true,
        sendNow,
      });
      rerender(<DesktopComposer />);
      fireEvent.keyDown(input(), { key: 'Enter' });
      expect(sendNow).toHaveBeenCalled();
    });

    it('hands focus to the store even with an empty field — the no-op is the store’s call', () => {
      // `beginEdit` is a no-op when there is nothing on screen to take over, so a
      // live mic survives a stray focus. The guard lives in the store (the phone's
      // transcript needs the same rule), not duplicated here.
      const beginEdit = vi.fn();
      mockVoiceValue = stubVoice({ beginEdit });
      render(<DesktopComposer />);

      fireEvent.focus(input());
      expect(beginEdit).toHaveBeenCalled();
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

  it('Escape gets you back out of the input, keeping the words', () => {
    const endEdit = vi.fn();
    mockVoiceValue = stubVoice({ settledText: 'half a thought', editing: true, endEdit });
    render(<DesktopComposer />);
    input().focus();

    fireEvent.keyDown(input(), { key: 'Escape' });
    expect(document.activeElement).not.toBe(input());
    // The text is the store's, so leaving the field cannot lose it — and the blur
    // ends the edit, which is what restarts a paused countdown.
    expect(input()).toHaveValue('half a thought');
    expect(endEdit).toHaveBeenCalled();
  });
});
