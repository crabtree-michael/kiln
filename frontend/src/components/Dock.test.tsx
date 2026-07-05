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
    sendNow: vi.fn(),
    getLevel: vi.fn(() => 0),
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

  it('shows the send + X whenever there is transcript text, and forwards taps', () => {
    const cancel = vi.fn();
    const sendNow = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'listening', tailText: 'never mind', cancel, sendNow });
    render(<Dock />);
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(sendNow).toHaveBeenCalledTimes(1);
    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it('shows the send + X with only settled ink (no interim tail)', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: 'Committed.', tailText: '' });
    render(<Dock />);
    expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
  });

  it('keeps the send + X available while paused (the "stuck" case)', () => {
    // Stop-listening froze the transcript; send/X must stay usable regardless of
    // mic state so the user can still send or clear it (09 §4, items 1 & 4).
    mockVoiceValue = stubVoice({ micState: 'paused', tailText: 'stuck text' });
    render(<Dock />);
    expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
  });

  it('hides the send + X when there is no transcript at all', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: '', tailText: '' });
    render(<Dock />);
    expect(screen.queryByRole('button', { name: 'Send' })).toBeNull();
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

  // The bottom-anchored-UI layering principle (see the web-client skill): the dock
  // expands upward as the transcript grows, and the notification hub must never
  // overlap it. The dock publishes its transcript overlay's height on the screen
  // root as `--dock-overlay-height`; the activity row offsets its `bottom` by that
  // var so it always clears the *current* dock height, collapsed or expanded.
  it('publishes the transcript overlay height on the screen root when expanded', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: 'a long utterance' });
    const { container } = render(
      <div data-role="primary-screen">
        <Dock />
      </div>,
    );
    const root = container.querySelector('[data-role="primary-screen"]');
    // Set (to the measured height — 0px under jsdom's null layout, but present),
    // which is what pushes the hub above the expanded dock.
    expect(root?.getAttribute('style') ?? '').toContain('--dock-overlay-height');
  });

  it('leaves no overlay offset when collapsed, so the hub sits on the dock', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: '', tailText: '' });
    const { container } = render(
      <div data-role="primary-screen">
        <Dock />
      </div>,
    );
    const root = container.querySelector('[data-role="primary-screen"]');
    expect(root?.getAttribute('style') ?? '').not.toContain('--dock-overlay-height');
  });
});
