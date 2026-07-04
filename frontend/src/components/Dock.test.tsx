// Dock tests (09 §3–§4): the dock is a presentational consumer of the voice
// store, so `useVoice` is mocked to a fixed value per case — deterministic, and
// no mic/socket I/O. Covers the four mic states' copy + `data-dock-state`, the
// mic tap (pause while listening / resume otherwise), the live transcript
// (settled + ghosted tail), and the X (cancel, only while there is an
// un-committed tail).
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { Dock } from '@/components/Dock';
import type { VoiceStoreValue } from '@/voice/voice-context';

let mockVoiceValue: VoiceStoreValue;

vi.mock('@/voice/voice-context', () => ({
  useVoice: (): VoiceStoreValue => mockVoiceValue,
}));

function stubVoice(overrides: Partial<VoiceStoreValue>): VoiceStoreValue {
  return {
    micState: 'listening',
    settledText: '',
    tailText: '',
    pause: vi.fn(),
    resume: vi.fn(),
    cancel: vi.fn(),
    ...overrides,
  };
}

describe('Dock', () => {
  beforeEach(() => {
    mockVoiceValue = stubVoice({});
  });

  it('Listening: shows "Listening…" and the listening state', () => {
    mockVoiceValue = stubVoice({ micState: 'listening' });
    render(<Dock />);
    expect(screen.getByRole('button', { name: 'Talk' })).toHaveAttribute(
      'data-dock-state',
      'listening',
    );
    expect(screen.getByText('Listening…')).toHaveAttribute('data-role', 'dock-label');
    expect(screen.getByRole('button', { name: 'Talk' })).toHaveAttribute('aria-pressed', 'true');
  });

  it('tapping the mic while listening calls pause', () => {
    const pause = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'listening', pause });
    render(<Dock />);
    fireEvent.click(screen.getByRole('button', { name: 'Talk' }));
    expect(pause).toHaveBeenCalledTimes(1);
  });

  it('Paused: shows "Tap to talk" and tapping the mic calls resume', () => {
    const resume = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'paused', resume });
    render(<Dock />);
    expect(screen.getByText('Tap to talk')).toHaveAttribute('data-role', 'dock-label');
    expect(screen.getByRole('button', { name: 'Talk' })).toHaveAttribute(
      'data-dock-state',
      'paused',
    );
    fireEvent.click(screen.getByRole('button', { name: 'Talk' }));
    expect(resume).toHaveBeenCalledTimes(1);
  });

  it('Denied: shows the enable-mic copy in the denied state', () => {
    mockVoiceValue = stubVoice({ micState: 'denied' });
    render(<Dock />);
    expect(screen.getByText('Tap to enable mic')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Talk' })).toHaveAttribute(
      'data-dock-state',
      'denied',
    );
  });

  it('Retry: shows "Tap to retry" and tapping the mic calls resume', () => {
    const resume = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'retry', resume });
    render(<Dock />);
    expect(screen.getByText('Tap to retry')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Talk' }));
    expect(resume).toHaveBeenCalledTimes(1);
  });

  it('renders the live transcript: settled in ink + ghosted tail', () => {
    mockVoiceValue = stubVoice({
      micState: 'listening',
      settledText: 'Hello, world.',
      tailText: 'and then',
    });
    render(<Dock />);
    expect(screen.getByText('Hello, world.')).toHaveAttribute('data-role', 'dock-settled');
    const tail = screen.getByText('and then');
    expect(tail).toHaveAttribute('data-role', 'dock-tail');
    expect(tail).toHaveAttribute('data-ghost', 'true');
  });

  it('shows the X and calls cancel while there is an un-committed tail', () => {
    const cancel = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'listening', tailText: 'never mind', cancel });
    render(<Dock />);
    const x = screen.getByRole('button', { name: 'Cancel' });
    fireEvent.click(x);
    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it('hides the X when there is no un-committed tail', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: 'Committed.', tailText: '' });
    render(<Dock />);
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull();
  });

  it('idle after send: no transcript region, mic controls intact', () => {
    // Post-send state (09 §4): the store cleared settledText + tailText, so the
    // dock shows an empty transcript ready for the next turn — but still listens.
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: '', tailText: '' });
    const { container } = render(<Dock />);
    expect(container.querySelector('[data-role="dock-transcript"]')).toBeNull();
    expect(screen.getByRole('button', { name: 'Talk' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull();
  });
});
