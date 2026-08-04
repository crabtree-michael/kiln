// The desktop input (13 §7, D5): typing is primary, voice is secondary, and both
// go through the one message seam. Like the dock's tests, the voice store is
// mocked to a fixed value per case — deterministic, and no mic/socket I/O.
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

  it('the field is there without asking — typing is the primary input at a desk', () => {
    render(<DesktopComposer />);
    expect(input()).toBeInTheDocument();
    // And it is one line, not a form: no title field, no priority select (13 §7).
    expect(screen.queryByRole('form')).toBeNull();
    expect(screen.queryByLabelText(/title/i)).toBeNull();
  });

  it('the mic is present as a secondary affordance on the line', () => {
    render(<DesktopComposer />);
    expect(screen.getByRole('button', { name: 'Talk' })).toBeInTheDocument();
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

  it('will not send an empty or whitespace-only draft', () => {
    const submitText = vi.fn(() => Promise.resolve(true));
    mockVoiceValue = stubVoice({ submitText });
    render(<DesktopComposer />);

    expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
    fireEvent.change(input(), { target: { value: '   ' } });
    fireEvent.keyDown(input(), { key: 'Enter' });
    expect(submitText).not.toHaveBeenCalled();
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

  it('shows the live transcript with send and clear when voice is mid-utterance', () => {
    const sendNow = vi.fn();
    const cancel = vi.fn();
    mockVoiceValue = stubVoice({
      micState: 'listening',
      settledText: 'add a retry to ',
      tailText: 'the poller',
      sendNow,
      cancel,
    });
    const { container } = render(<DesktopComposer />);

    expect(container.querySelector('[data-role="desktop-transcript"]')?.textContent).toContain(
      'add a retry to the poller',
    );
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    expect(sendNow).toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Clear' }));
    expect(cancel).toHaveBeenCalled();
  });

  it("keeps the mic tappable while a transcript is up, unlike the sheet's send-aware mic", () => {
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: 'something' });
    render(<DesktopComposer />);
    expect(screen.getByRole('button', { name: 'Talk' })).toBeInTheDocument();
  });

  it('renders no transcript block at rest', () => {
    const { container } = render(<DesktopComposer />);
    expect(container.querySelector('[data-role="desktop-transcript"]')).toBeNull();
  });
});
