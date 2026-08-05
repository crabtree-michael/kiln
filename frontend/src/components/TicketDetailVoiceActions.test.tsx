// TicketDetailVoiceActions tests (08 §5, 09 §3–§4): the ticket sheet's footer
// voice cluster is a presentational consumer of the voice store, so `useVoice` is
// mocked to a fixed value per case — deterministic, no mic/socket I/O.
//
// What it has to get right: the mic stays on screen for the whole session (it is
// the expression indicator), Send and × appear beside it only while one is live,
// the trailing group reads Send → × → mic from the right edge inward, and the ×
// is the way OUT (discard AND stop the mic) rather than the dock's clear-and-keep-
// listening. Plus the `onActiveChange` reporting the sheet above rearranges on.
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { TicketDetailVoiceActions } from '@/components/TicketDetailVoiceActions';
import type { VoiceStoreValue } from '@/voice/voice-context';

let mockVoiceValue: VoiceStoreValue;

vi.mock('@/voice/voice-context', () => ({
  useVoice: (): VoiceStoreValue => mockVoiceValue,
}));

function stubVoice(overrides: Partial<VoiceStoreValue>): VoiceStoreValue {
  return {
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

describe('TicketDetailVoiceActions', () => {
  beforeEach(() => {
    mockVoiceValue = stubVoice({});
  });

  it('is the mic alone at rest — no Send, no × to reach past', () => {
    mockVoiceValue = stubVoice({ micState: 'paused' });
    render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" />);
    expect(screen.getByRole('button', { name: 'Talk' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Send' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Discard' })).toBeNull();
  });

  it('shows Send and × the moment the mic goes up, and keeps the mic on screen', () => {
    // Tapped on, nothing said yet: the session is live, so the actions are there —
    // and the orb has to stay, since its glow is the only thing reporting that the
    // mic is hearing anything.
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: '', tailText: '' });
    const { container } = render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" />);
    expect(container.querySelector('[data-role="dock-mic-orb"]')).not.toBeNull();
    expect(screen.getByRole('button', { name: 'Send' })).toHaveAttribute('data-role', 'dock-send');
    expect(screen.getByRole('button', { name: 'Discard' })).toHaveAttribute(
      'data-role',
      'dock-cancel',
    );
  });

  it('reads Send, ×, mic from the right edge inward', () => {
    // DOM order is the reverse of the reading order, because the cluster sits at
    // the row's trailing end: mic first, then ×, then Send hard against the edge —
    // the slot Accept vacates for the session.
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: 'move the button' });
    const { container } = render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" />);
    const roles = [...container.querySelectorAll('button')].map((el) =>
      el.getAttribute('data-role'),
    );
    expect(roles).toEqual(['dock-talk', 'dock-cancel', 'dock-send']);
  });

  it('keeps the session live on a still-forming tail alone', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: '', tailText: 'move the' });
    render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" />);
    expect(screen.getByRole('button', { name: 'Send' })).toBeEnabled();
  });

  it('stays in the speaking arrangement on a paused mic that still holds words', () => {
    // An end-of-turn final pauses the mic while the utterance sits armed in the
    // grace window — the footer must not snap back to Accept underneath it.
    mockVoiceValue = stubVoice({ micState: 'paused', settledText: 'move the button' });
    render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" />);
    expect(screen.getByRole('button', { name: 'Send' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Discard' })).toBeInTheDocument();
  });

  it('holds Send in place but dead before there is anything to send', () => {
    // Present-but-disabled rather than absent, so × and the mic do not shuffle
    // sideways the instant the first partial lands.
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: '', tailText: '' });
    render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" />);
    expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
  });

  it('Send commits whatever is on screen now', () => {
    const sendNow = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: 'move the button', sendNow });
    render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" />);
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    expect(sendNow).toHaveBeenCalledTimes(1);
  });

  it('× discards AND stops the mic — it is the way out of the mode', () => {
    // The dock's × deliberately leaves the mic listening; this one must not, or
    // the footer stays in the speaking arrangement with nothing said and no
    // obvious way back to Accept.
    const cancel = vi.fn();
    const pause = vi.fn();
    mockVoiceValue = stubVoice({
      micState: 'listening',
      settledText: 'never mind',
      cancel,
      pause,
    });
    render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" />);
    fireEvent.click(screen.getByRole('button', { name: 'Discard' }));
    expect(cancel).toHaveBeenCalledTimes(1);
    expect(pause).toHaveBeenCalledTimes(1);
  });

  it('reports the session state up, on mount and on every flip', () => {
    const onActiveChange = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'paused' });
    const { rerender } = render(
      <TicketDetailVoiceActions ticketTitle="Ship the redesign" onActiveChange={onActiveChange} />,
    );
    expect(onActiveChange).toHaveBeenLastCalledWith(false);

    mockVoiceValue = stubVoice({ micState: 'listening' });
    rerender(
      <TicketDetailVoiceActions ticketTitle="Ship the redesign" onActiveChange={onActiveChange} />,
    );
    expect(onActiveChange).toHaveBeenLastCalledWith(true);
  });

  it('reports the session ended when the sheet closes mid-utterance', () => {
    // The flag lives on the screen above, which does not unmount with the sheet —
    // so a session left live at close would strand the next ticket's footer in the
    // speaking arrangement.
    const onActiveChange = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: 'half a thought' });
    const { unmount } = render(
      <TicketDetailVoiceActions ticketTitle="Ship the redesign" onActiveChange={onActiveChange} />,
    );
    expect(onActiveChange).toHaveBeenLastCalledWith(true);
    unmount();
    expect(onActiveChange).toHaveBeenLastCalledWith(false);
  });

  it('registers the ticket with the voice store so a message sent from here is prefixed', () => {
    const setTicketContext = vi.fn();
    mockVoiceValue = stubVoice({ setTicketContext });
    const { unmount } = render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" />);
    expect(setTicketContext).toHaveBeenCalledWith('Ship the redesign');
    unmount();
    expect(setTicketContext).toHaveBeenLastCalledWith(null);
  });
});
