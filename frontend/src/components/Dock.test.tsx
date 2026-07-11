// Dock tests (09 §3–§4): the dock is a presentational consumer of the voice
// store, so `useVoice` is mocked to a fixed value per case — deterministic, and
// no mic/socket I/O. Covers the four mic states' copy + `data-dock-state`, the
// mic tap (pause while listening / resume otherwise), the live transcript
// (settled + ghosted tail), and the X (cancel, only while there is an
// un-committed tail).
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Dock } from '@/components/Dock';
import { makeSystemAlert } from '@/test/fixtures';
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
    getLevel: vi.fn(() => 0),
    keyboardMode: false,
    openKeyboard: vi.fn(),
    closeKeyboard: vi.fn(),
    submitText: vi.fn(() => Promise.resolve(true)),
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

  it('Connecting: shows a spinner around the mic and "Connecting…" copy', () => {
    // The mic is tapped on (listening) but the socket isn't recording yet: the
    // dock flags the setup window and swaps the live glow for a spinner so the
    // user waits to speak (09 §3).
    mockVoiceValue = stubVoice({ micState: 'listening', connecting: true });
    const { container } = render(<Dock />);
    const talk = screen.getByRole('button', { name: 'Talk' });
    expect(talk).toHaveAttribute('data-dock-connecting', 'true');
    expect(container.querySelector('[data-role="dock-mic-spinner"]')).not.toBeNull();
    expect(screen.getByText('Connecting…')).toHaveAttribute('data-role', 'dock-label');
  });

  it('not connecting: no spinner and the resting mic copy', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', connecting: false });
    const { container } = render(<Dock />);
    expect(screen.getByRole('button', { name: 'Talk' })).not.toHaveAttribute(
      'data-dock-connecting',
    );
    expect(container.querySelector('[data-role="dock-mic-spinner"]')).toBeNull();
    expect(screen.getByText('Listening…')).toHaveAttribute('data-role', 'dock-label');
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

  it('shows "+10" in the final stretch before the auto-send fires, and forwards the tap', () => {
    const delaySend = vi.fn();
    mockVoiceValue = stubVoice({
      micState: 'listening',
      settledText: 'Move it to done.',
      countingDown: true,
      sendImminent: true,
      delaySend,
    });
    render(<Dock />);
    fireEvent.click(screen.getByRole('button', { name: 'Delay auto-send 10 seconds' }));
    expect(delaySend).toHaveBeenCalledTimes(1);
  });

  it('hides "+10" while counting down but not yet in the final stretch', () => {
    // Just after a "+10" tap the deadline is pushed out past the reveal stretch: the
    // send is still armed (countingDown) but not imminent, so the control withdraws
    // until the countdown runs back down into the stretch.
    mockVoiceValue = stubVoice({
      micState: 'listening',
      settledText: 'Move it to done.',
      countingDown: true,
      sendImminent: false,
    });
    render(<Dock />);
    expect(screen.queryByRole('button', { name: 'Delay auto-send 10 seconds' })).toBeNull();
  });

  it('hides "+10" when there is transcript but no countdown (the "stuck" case)', () => {
    // A frozen/paused transcript can still be sent or cleared, but nothing is about
    // to auto-fire — so the delay control has no countdown to extend and stays hidden.
    mockVoiceValue = stubVoice({
      micState: 'paused',
      settledText: 'stuck',
      countingDown: false,
      sendImminent: false,
    });
    render(<Dock />);
    expect(screen.queryByRole('button', { name: 'Delay auto-send 10 seconds' })).toBeNull();
    // The send + X remain available in this state.
    expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
  });

  it('hides the send + X when there is no transcript at all', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: '', tailText: '' });
    render(<Dock />);
    expect(screen.queryByRole('button', { name: 'Send' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull();
  });

  it('idle after send: back to Paused ("Tap to talk"), no transcript, mic controls intact', () => {
    // Post-send state: sending stops the mic, so the store cleared settledText +
    // tailText and dropped to Paused. The dock shows an empty transcript and the
    // resting "Tap to talk" mic — the user taps to speak the next message.
    mockVoiceValue = stubVoice({ micState: 'paused', settledText: '', tailText: '' });
    const { container } = render(<Dock />);
    expect(container.querySelector('[data-role="dock-transcript"]')).toBeNull();
    expect(screen.getByText('Tap to talk')).toBeInTheDocument();
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

  // Regression (sandbox-health band occlusion): the persistent error band lives
  // INSIDE the dock as its first in-flow child, above the controls — so the
  // transcript overlay, anchored to the dock's top edge (`bottom: 100%`), grows
  // ABOVE the band instead of painting over it. Moving the band back out to a
  // dock-region sibling, or nesting it under the transcript, re-introduces the
  // bug: the opaque overlay occludes the band and the toast row floats a
  // band-height too high (whitespace between the input and the toast). jsdom has
  // no layout, so the pixel geometry is verified in the browser; this locks the
  // DOM contract the CSS occlusion-avoidance depends on.
  it('renders the error band as the dock’s first child, sibling to (never under) the transcript overlay, so a live transcript cannot occlude it', () => {
    mockVoiceValue = stubVoice({ keyboardMode: true });
    const { container } = render(<Dock alerts={[makeSystemAlert('1 of 3 sandboxes failing')]} />);

    const dock = container.querySelector('[data-role="dock"]');
    const band = container.querySelector('[data-role="system-alert-band"]');
    const transcript = container.querySelector('[data-role="dock-transcript"]');

    // The band shows its message and the transcript overlay is up at the same time.
    expect(band).toHaveTextContent('1 of 3 sandboxes failing');
    expect(transcript).not.toBeNull();

    // The band is a direct child of the dock — not a dock-region sibling — and the
    // transcript is a peer of it, NOT an ancestor (nesting would let the opaque
    // overlay cover it).
    expect(band?.parentElement).toBe(dock);
    expect(transcript?.parentElement).toBe(dock);
    expect(transcript?.contains(band)).toBe(false);

    // First in-flow child: the band is the dock's first element, so the dock's top
    // edge is the band's top and the transcript's `bottom: 100%` anchors there,
    // growing upward — clear of the band, ahead of the controls row below.
    expect(dock?.firstElementChild).toBe(band);
    expect(container.querySelector('[data-role="dock-controls"]')).not.toBeNull();
  });

  // As text streams in the transcript can overflow its cap (`max-height: 28vh`,
  // `overflow-y: auto`); text flows top-to-bottom so the newest words sit at the
  // bottom. The dock re-pins `scrollTop` to `scrollHeight` on every settled/tail
  // update so the latest words stay in view without manual scrolling.
  it('auto-scrolls the transcript to the bottom as text streams in', () => {
    // jsdom reports 0 for layout metrics, so stand in a growing scrollHeight.
    const scrollHeight = vi
      .spyOn(HTMLElement.prototype, 'scrollHeight', 'get')
      .mockReturnValue(600);
    try {
      mockVoiceValue = stubVoice({ micState: 'listening', settledText: 'first words' });
      const { container, rerender } = render(<Dock />);
      const transcript = container.querySelector<HTMLElement>('[data-role="dock-transcript"]');
      expect(transcript?.scrollTop).toBe(600);

      // A new partial streams in: the tail grows and the pin re-runs to the new bottom.
      scrollHeight.mockReturnValue(900);
      mockVoiceValue = stubVoice({
        micState: 'listening',
        settledText: 'first words',
        tailText: 'still going',
      });
      rerender(<Dock />);
      expect(transcript?.scrollTop).toBe(900);
    } finally {
      scrollHeight.mockRestore();
    }
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

  // Keyboard input (the alternate to voice): a right-side toggle opens a typed
  // field in place of the live transcript; submitting rides the same downstream
  // seam as a spoken utterance. Voice stays the default — the toggle only shows in
  // the resting state, never mid-dictation.
  describe('keyboard input', () => {
    it('shows the keyboard toggle in the resting state and forwards taps', () => {
      const openKeyboard = vi.fn();
      mockVoiceValue = stubVoice({
        micState: 'listening',
        settledText: '',
        tailText: '',
        openKeyboard,
      });
      render(<Dock />);
      fireEvent.click(screen.getByRole('button', { name: 'Type a message' }));
      expect(openKeyboard).toHaveBeenCalledTimes(1);
    });

    it('hides the keyboard toggle while a transcript is on screen', () => {
      mockVoiceValue = stubVoice({ micState: 'listening', tailText: 'mid thought' });
      render(<Dock />);
      expect(screen.queryByRole('button', { name: 'Type a message' })).toBeNull();
    });

    it('hides the keyboard toggle once keyboard mode is open', () => {
      mockVoiceValue = stubVoice({ keyboardMode: true });
      render(<Dock />);
      expect(screen.queryByRole('button', { name: 'Type a message' })).toBeNull();
    });

    it('renders the typed field (not the transcript) in keyboard mode, and the centre mic glyph is gone', () => {
      mockVoiceValue = stubVoice({ keyboardMode: true });
      const { container } = render(<Dock />);
      expect(screen.getByRole('textbox', { name: 'Message' })).toBeInTheDocument();
      // The volume-reactive central mic glyph (dock-talk) is absent; the left
      // slot instead holds the voice toggle that re-enables the mic.
      expect(container.querySelector('[data-role="dock-talk"]')).toBeNull();
    });

    it('submits the typed text through submitText and clears the field on success', async () => {
      const submitText = vi.fn(() => Promise.resolve(true));
      mockVoiceValue = stubVoice({ keyboardMode: true, submitText });
      render(<Dock />);
      const field = screen.getByRole('textbox', { name: 'Message' });
      fireEvent.change(field, { target: { value: 'ship it' } });
      fireEvent.click(screen.getByRole('button', { name: 'Send' }));
      expect(submitText).toHaveBeenCalledWith('ship it');
      await waitFor(() => {
        expect(field).toHaveValue('');
      });
    });

    it('keeps the typed text in the field when the POST fails', async () => {
      const submitText = vi.fn(() => Promise.resolve(false));
      mockVoiceValue = stubVoice({ keyboardMode: true, submitText });
      render(<Dock />);
      const field = screen.getByRole('textbox', { name: 'Message' });
      fireEvent.change(field, { target: { value: 'try again' } });
      fireEvent.click(screen.getByRole('button', { name: 'Send' }));
      await waitFor(() => {
        expect(submitText).toHaveBeenCalledTimes(1);
      });
      expect(field).toHaveValue('try again');
    });

    it('submits on Enter and inserts a newline on Shift+Enter', () => {
      const submitText = vi.fn(() => Promise.resolve(true));
      mockVoiceValue = stubVoice({ keyboardMode: true, submitText });
      render(<Dock />);
      const field = screen.getByRole('textbox', { name: 'Message' });
      fireEvent.change(field, { target: { value: 'hello' } });
      fireEvent.keyDown(field, { key: 'Enter', shiftKey: true });
      expect(submitText).not.toHaveBeenCalled();
      fireEvent.keyDown(field, { key: 'Enter' });
      expect(submitText).toHaveBeenCalledWith('hello');
    });

    it('does not submit an empty/whitespace draft', () => {
      const submitText = vi.fn(() => Promise.resolve(true));
      mockVoiceValue = stubVoice({ keyboardMode: true, submitText });
      render(<Dock />);
      const field = screen.getByRole('textbox', { name: 'Message' });
      fireEvent.change(field, { target: { value: '   ' } });
      // The send button is disabled and Enter is a no-op for a blank draft.
      expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
      fireEvent.keyDown(field, { key: 'Enter' });
      expect(submitText).not.toHaveBeenCalled();
    });

    it('offers the clear (×) only once there is draft text, and wipes the field on tap', () => {
      mockVoiceValue = stubVoice({ keyboardMode: true });
      render(<Dock />);
      const field = screen.getByRole('textbox', { name: 'Message' });
      // Nothing typed yet — no clear affordance.
      expect(screen.queryByRole('button', { name: 'Clear text' })).toBeNull();
      fireEvent.change(field, { target: { value: 'scratch this' } });
      const clear = screen.getByRole('button', { name: 'Clear text' });
      fireEvent.click(clear);
      // The field is emptied and the button retires with the text it cleared;
      // clearing does not send or leave keyboard mode.
      expect(field).toHaveValue('');
      expect(screen.queryByRole('button', { name: 'Clear text' })).toBeNull();
    });

    it('leaves keyboard mode and turns the mic back on via the voice button', () => {
      const closeKeyboard = vi.fn();
      const resume = vi.fn();
      mockVoiceValue = stubVoice({ keyboardMode: true, closeKeyboard, resume });
      render(<Dock />);
      fireEvent.click(screen.getByRole('button', { name: 'Talk' }));
      expect(closeKeyboard).toHaveBeenCalledTimes(1);
      expect(resume).toHaveBeenCalledTimes(1);
    });

    it('shows the dismiss button while the field is focused (keyboard up)', () => {
      mockVoiceValue = stubVoice({ keyboardMode: true });
      render(<Dock />);
      // The field auto-focuses on entering keyboard mode, so the soft keyboard is
      // up and the dismiss control is offered.
      expect(screen.getByRole('button', { name: 'Dismiss keyboard' })).toBeInTheDocument();
    });

    it('dismisses the keyboard by blurring the field, staying in keyboard mode', () => {
      const closeKeyboard = vi.fn();
      mockVoiceValue = stubVoice({ keyboardMode: true, closeKeyboard });
      render(<Dock />);
      const field = screen.getByRole('textbox', { name: 'Message' });
      expect(field).toHaveFocus();
      fireEvent.click(screen.getByRole('button', { name: 'Dismiss keyboard' }));
      // The field blurs (keyboard closes) but we do not leave keyboard mode.
      expect(field).not.toHaveFocus();
      expect(closeKeyboard).not.toHaveBeenCalled();
      // With the keyboard down the dismiss button is gone; the field remains.
      expect(screen.queryByRole('button', { name: 'Dismiss keyboard' })).toBeNull();
      expect(field).toBeInTheDocument();
    });

    it('does not offer the dismiss button outside keyboard mode', () => {
      mockVoiceValue = stubVoice({ micState: 'listening', settledText: 'hello' });
      render(<Dock />);
      expect(screen.queryByRole('button', { name: 'Dismiss keyboard' })).toBeNull();
    });
  });
});
