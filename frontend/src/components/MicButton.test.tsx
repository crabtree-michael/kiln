// MicButton tests (09 §3): the shared mic-orb button is a presentational consumer
// of the voice store, so `useVoice` is mocked to a fixed value per case —
// deterministic, no mic/socket I/O. Covers the mic tap (pause while listening /
// resume otherwise), the connecting spinner, and the optional state-copy label
// (shown in the dock, omitted in compact placements like the proposal sheet).
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
    delaySend: vi.fn(),
    getLevel: vi.fn(() => 0),
    keyboardMode: false,
    openKeyboard: vi.fn(),
    closeKeyboard: vi.fn(),
    submitText: vi.fn(() => Promise.resolve(true)),
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

  it('omits the state-copy label by default (compact placement)', () => {
    mockVoiceValue = stubVoice({ micState: 'paused' });
    const { container } = render(<MicButton />);
    expect(container.querySelector('[data-role="dock-label"]')).toBeNull();
    expect(screen.queryByText('Tap to talk')).toBeNull();
  });

  it('shows the state-copy label when asked (the dock)', () => {
    mockVoiceValue = stubVoice({ micState: 'paused' });
    render(<MicButton showLabel />);
    expect(screen.getByText('Tap to talk')).toHaveAttribute('data-role', 'dock-label');
  });

  it('shows the connecting copy over the state label while connecting', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', connecting: true });
    render(<MicButton showLabel />);
    expect(screen.getByText('Connecting…')).toHaveAttribute('data-role', 'dock-label');
  });
});
