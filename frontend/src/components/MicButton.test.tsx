// MicButton tests (09 §3): the shared mic-orb button is a presentational consumer
// of the voice store, so `useVoice` is mocked to a fixed value per case —
// deterministic, no mic/socket I/O. Covers the mic tap (pause while listening /
// resume otherwise), the connecting spinner, and the ticket-context registration.
// The send/discard actions that used to live here behind `sendable` are now the
// sheet's own cluster — see TicketDetailVoiceActions.test.tsx.
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MicButton } from '@/components/MicButton';
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

describe('MicButton', () => {
  beforeEach(() => {
    mockVoiceValue = stubVoice({});
  });

  it('renders the mic orb and reflects the mic state', () => {
    mockVoiceValue = stubVoice({ micState: 'listening' });
    const { container } = render(<MicButton />);
    const talk = screen.getByRole('button', { name: 'Talk' });
    expect(talk).toHaveAttribute('data-dock-state', 'listening');
    expect(talk).toHaveAttribute('aria-pressed', 'true');
    expect(container.querySelector('[data-role="dock-mic-orb"]')).not.toBeNull();
  });

  it('tapping while listening calls pause', () => {
    const pause = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'listening', pause });
    render(<MicButton />);
    fireEvent.click(screen.getByRole('button', { name: 'Talk' }));
    expect(pause).toHaveBeenCalledTimes(1);
  });

  it('tapping while paused starts a session (resume)', () => {
    const resume = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'paused', resume });
    render(<MicButton />);
    const talk = screen.getByRole('button', { name: 'Talk' });
    expect(talk).toHaveAttribute('aria-pressed', 'false');
    fireEvent.click(talk);
    expect(resume).toHaveBeenCalledTimes(1);
  });

  it('shows the connecting spinner during the setup window', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', connecting: true });
    const { container } = render(<MicButton />);
    expect(screen.getByRole('button', { name: 'Talk' })).toHaveAttribute(
      'data-dock-connecting',
      'true',
    );
    expect(container.querySelector('[data-role="dock-mic-spinner"]')).not.toBeNull();
  });

  it('renders no state-copy label (mic orb only)', () => {
    mockVoiceValue = stubVoice({ micState: 'paused' });
    const { container } = render(<MicButton />);
    expect(container.querySelector('[data-role="dock-label"]')).toBeNull();
    expect(screen.queryByText('Tap to talk')).toBeNull();
    expect(screen.queryByText('Listening…')).toBeNull();
  });

  // The orb is the whole component on every surface: a transcript on screen never
  // takes it away, because each placement (the dock's controls row, the desktop
  // composer, the sheet's TicketDetailVoiceActions) renders its own send/discard
  // AROUND it. It used to carry a `sendable` mode that swapped the orb out for
  // those two — gone, since the sheet now keeps the mic visible while speaking.
  it('stays a mic orb while a transcript is on screen — it renders no send/discard of its own', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: 'hello', tailText: 'and' });
    const { container } = render(<MicButton />);
    expect(container.querySelector('[data-role="dock-mic-orb"]')).not.toBeNull();
    expect(screen.queryByRole('button', { name: 'Send' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Discard' })).toBeNull();
  });

  it('registers the ticket context when placed inside a sheet and clears it on unmount', () => {
    const setTicketContext = vi.fn();
    mockVoiceValue = stubVoice({ setTicketContext });
    const { unmount } = render(<MicButton ticketContext="Ship the redesign" />);
    expect(setTicketContext).toHaveBeenCalledWith('Ship the redesign');
    unmount();
    expect(setTicketContext).toHaveBeenLastCalledWith(null);
  });

  it('leaves the ticket context untouched in the dock (no ticketContext prop)', () => {
    const setTicketContext = vi.fn();
    mockVoiceValue = stubVoice({ setTicketContext });
    render(<MicButton />);
    expect(setTicketContext).not.toHaveBeenCalled();
  });
});
