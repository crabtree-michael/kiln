// TicketDetailVoiceActions tests (08 §5, 09 §3–§4): the ticket sheet's footer
// voice cluster is a presentational consumer of the voice store, so `useVoice` is
// mocked to a fixed value per case — deterministic, no mic/socket I/O.
//
// What it has to get right: the mic stays on screen for the whole session (it is
// the expression indicator), Send and × appear beside it only while one is live,
// the trailing group reads Send → × → mic from the right edge inward, and the ×
// is the way OUT (discard AND stop the mic) rather than the dock's clear-and-keep-
// listening. Plus the `onActiveChange` reporting the sheet above rearranges on.
//
// The keyboard toggle rides the same rules from the other side: it is offered
// beside the mic at rest and only at rest, and once typed input is live it wears
// the SAME Send and × the spoken utterance does — pointed at the draft. The draft
// itself is owned above (`useTicketKeyboard`), so it is stubbed here per case
// exactly as the voice store is.
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { createRef } from 'react';
import { TicketDetailVoiceActions } from '@/components/TicketDetailVoiceActions';
import type { TicketKeyboard } from '@/components/use-ticket-keyboard';
import type { VoiceStoreValue } from '@/voice/voice-context';

let mockVoiceValue: VoiceStoreValue;
let mockKeyboard: TicketKeyboard;

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

describe('TicketDetailVoiceActions', () => {
  beforeEach(() => {
    mockVoiceValue = stubVoice({});
    mockKeyboard = stubKeyboard({});
  });

  it('is the mic and the keyboard toggle at rest — no Send, no × to reach past', () => {
    mockVoiceValue = stubVoice({ micState: 'paused' });
    render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" keyboard={mockKeyboard} />);
    expect(screen.getByRole('button', { name: 'Talk' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Type a message' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Send' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Discard' })).toBeNull();
  });

  it('keeps the keyboard toggle in the mic’s own group, so neither one travels', () => {
    // The toggle is the mic's neighbour, not a third loose child of the cluster —
    // `space-between` would fling a loose one to the row's far end, next to
    // Accept. Grouped, the mic stays the group's first child and the group stays
    // the cluster's first child, which is what pins the mic to the row's left
    // edge in every reading.
    const { container } = render(
      <TicketDetailVoiceActions ticketTitle="Ship the redesign" keyboard={mockKeyboard} />,
    );
    const lead = container.querySelector('[data-role="ticket-detail-voice-mic"]');
    expect(lead).not.toBeNull();
    const roles = [...(lead?.querySelectorAll('button') ?? [])].map((el) =>
      el.getAttribute('data-role'),
    );
    expect(roles).toEqual(['dock-talk', 'dock-keyboard']);
  });

  it('tapping the keyboard toggle opens typed input', () => {
    const begin = vi.fn();
    mockKeyboard = stubKeyboard({ begin });
    render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" keyboard={mockKeyboard} />);
    fireEvent.click(screen.getByRole('button', { name: 'Type a message' }));
    expect(begin).toHaveBeenCalledTimes(1);
  });

  it('withdraws the keyboard toggle the moment either input goes live', () => {
    // Speaking: the mic orb is already the way out, and the trailing slot has the
    // session's Send and ×.
    mockVoiceValue = stubVoice({ micState: 'listening' });
    const { unmount } = render(
      <TicketDetailVoiceActions ticketTitle="Ship the redesign" keyboard={mockKeyboard} />,
    );
    expect(screen.queryByRole('button', { name: 'Type a message' })).toBeNull();
    unmount();

    // Typing: same reading — tapping the mic is how you go back to voice, so a
    // toggle beside it would be a second way to change the same mind.
    mockVoiceValue = stubVoice({ micState: 'paused' });
    mockKeyboard = stubKeyboard({ open: true });
    render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" keyboard={mockKeyboard} />);
    expect(screen.queryByRole('button', { name: 'Type a message' })).toBeNull();
  });

  it('hands the trailing slot to a typed message on the same terms as a spoken one', () => {
    // Typed input is a live session, so the sheet withdraws Poke/Delete/Accept
    // for it exactly as it does for an utterance — same swap, same slot.
    const onActiveChange = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'paused', settledText: '', tailText: '' });
    mockKeyboard = stubKeyboard({ open: true });
    render(
      <TicketDetailVoiceActions
        ticketTitle="Ship the redesign"
        keyboard={mockKeyboard}
        onActiveChange={onActiveChange}
      />,
    );
    expect(onActiveChange).toHaveBeenLastCalledWith(true);
    expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Discard' })).toBeInTheDocument();
    // ...and the mic never left, so the ticket-context registration behind a
    // typed message is still standing.
    expect(screen.getByRole('button', { name: 'Talk' })).toBeInTheDocument();
  });

  it('Send posts the typed draft, and is dead until there is one', () => {
    const submit = vi.fn();
    const sendNow = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'paused', sendNow });
    mockKeyboard = stubKeyboard({ open: true, draft: '   ', submit });
    const { rerender } = render(
      <TicketDetailVoiceActions ticketTitle="Ship the redesign" keyboard={mockKeyboard} />,
    );
    // Whitespace is not a message: present, holding its place, and dead.
    expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();

    mockKeyboard = stubKeyboard({ open: true, draft: 'move the button', submit });
    rerender(<TicketDetailVoiceActions ticketTitle="Ship the redesign" keyboard={mockKeyboard} />);
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    expect(submit).toHaveBeenCalledTimes(1);
    // The typed seam, not the utterance one — there is no transcript to commit.
    expect(sendNow).not.toHaveBeenCalled();
  });

  it('× drops the draft and closes the field — the same way out, in typed input', () => {
    const end = vi.fn();
    const cancel = vi.fn();
    const pause = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'paused', cancel, pause });
    mockKeyboard = stubKeyboard({ open: true, draft: 'never mind', end });
    render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" keyboard={mockKeyboard} />);
    fireEvent.click(screen.getByRole('button', { name: 'Discard' }));
    expect(end).toHaveBeenCalledTimes(1);
    // The voice teardown is not the typed one: there is no utterance to discard
    // and no mic to stop.
    expect(cancel).not.toHaveBeenCalled();
    expect(pause).not.toHaveBeenCalled();
  });

  it('shows Send and × the moment the mic goes up, and keeps the mic on screen', () => {
    // Tapped on, nothing said yet: the session is live, so the actions are there —
    // and the orb has to stay, since its glow is the only thing reporting that the
    // mic is hearing anything.
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: '', tailText: '' });
    const { container } = render(
      <TicketDetailVoiceActions ticketTitle="Ship the redesign" keyboard={mockKeyboard} />,
    );
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
    const { container } = render(
      <TicketDetailVoiceActions ticketTitle="Ship the redesign" keyboard={mockKeyboard} />,
    );
    const roles = [...container.querySelectorAll('button')].map((el) =>
      el.getAttribute('data-role'),
    );
    expect(roles).toEqual(['dock-talk', 'dock-cancel', 'dock-send']);
  });

  it('keeps the session live on a still-forming tail alone', () => {
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: '', tailText: 'move the' });
    render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" keyboard={mockKeyboard} />);
    expect(screen.getByRole('button', { name: 'Send' })).toBeEnabled();
  });

  it('stays in the speaking arrangement on a paused mic that still holds words', () => {
    // An end-of-turn final pauses the mic while the utterance sits armed in the
    // grace window — the footer must not snap back to Accept underneath it.
    mockVoiceValue = stubVoice({ micState: 'paused', settledText: 'move the button' });
    render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" keyboard={mockKeyboard} />);
    expect(screen.getByRole('button', { name: 'Send' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Discard' })).toBeInTheDocument();
  });

  it('holds Send in place but dead before there is anything to send', () => {
    // Present-but-disabled rather than absent, so × and the mic do not shuffle
    // sideways the instant the first partial lands.
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: '', tailText: '' });
    render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" keyboard={mockKeyboard} />);
    expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
  });

  it('Send commits whatever is on screen now', () => {
    const sendNow = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'listening', settledText: 'move the button', sendNow });
    render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" keyboard={mockKeyboard} />);
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
    render(<TicketDetailVoiceActions ticketTitle="Ship the redesign" keyboard={mockKeyboard} />);
    fireEvent.click(screen.getByRole('button', { name: 'Discard' }));
    expect(cancel).toHaveBeenCalledTimes(1);
    expect(pause).toHaveBeenCalledTimes(1);
  });

  it('reports the session state up, on mount and on every flip', () => {
    const onActiveChange = vi.fn();
    mockVoiceValue = stubVoice({ micState: 'paused' });
    const { rerender } = render(
      <TicketDetailVoiceActions
        ticketTitle="Ship the redesign"
        keyboard={mockKeyboard}
        onActiveChange={onActiveChange}
      />,
    );
    expect(onActiveChange).toHaveBeenLastCalledWith(false);

    mockVoiceValue = stubVoice({ micState: 'listening' });
    rerender(
      <TicketDetailVoiceActions
        ticketTitle="Ship the redesign"
        keyboard={mockKeyboard}
        onActiveChange={onActiveChange}
      />,
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
      <TicketDetailVoiceActions
        ticketTitle="Ship the redesign"
        keyboard={mockKeyboard}
        onActiveChange={onActiveChange}
      />,
    );
    expect(onActiveChange).toHaveBeenLastCalledWith(true);
    unmount();
    expect(onActiveChange).toHaveBeenLastCalledWith(false);
  });

  it('registers the ticket with the voice store so a message sent from here is prefixed', () => {
    const setTicketContext = vi.fn();
    mockVoiceValue = stubVoice({ setTicketContext });
    const { unmount } = render(
      <TicketDetailVoiceActions ticketTitle="Ship the redesign" keyboard={mockKeyboard} />,
    );
    expect(setTicketContext).toHaveBeenCalledWith('Ship the redesign');
    unmount();
    expect(setTicketContext).toHaveBeenLastCalledWith(null);
  });
});
