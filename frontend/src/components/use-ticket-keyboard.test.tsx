// `useTicketKeyboard` (08 §5, 09 §4): the ticket sheet's typed-input state, held
// in the host because the sheet's dock renders it in two slots.
//
// What it has to get right is mostly about what typed input must NOT do — leak
// into the dock's own typing mode, survive the ticket it was written against, or
// coexist with a live mic. `useVoice` is mocked per case, as everywhere else in
// the sheet's dock, so there is no mic or socket I/O.
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { act, renderHook, waitFor } from '@testing-library/react';
import { useTicketKeyboard, type TicketKeyboard } from '@/components/use-ticket-keyboard';
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

describe('useTicketKeyboard', () => {
  beforeEach(() => {
    mockVoiceValue = stubVoice({});
  });

  it('opens closed, with nothing typed', () => {
    const { result } = renderHook(() => useTicketKeyboard('t1'));
    expect(result.current.open).toBe(false);
    expect(result.current.draft).toBe('');
  });

  it('stops the mic and drops what it heard when typed input begins', () => {
    // One input at a time: a spoken half-sentence left in the buffer would ride
    // out with the typed message, or land on its own the next time a send armed.
    const cancel = vi.fn();
    // Model the real store: `pause` stops the mic *synchronously*, in the same
    // handler and so in the same React batch as the mode opening. That is what
    // keeps the "mic went live → stand down" rule below from firing on the very
    // render that opens the field.
    const pause = vi.fn(() => {
      mockVoiceValue = { ...mockVoiceValue, micState: 'paused' };
    });
    mockVoiceValue = stubVoice({ micState: 'listening', pause, cancel });
    const { result } = renderHook(() => useTicketKeyboard('t1'));

    act(() => {
      result.current.begin();
    });
    expect(result.current.open).toBe(true);
    expect(pause).toHaveBeenCalledTimes(1);
    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it('never touches the dock’s own keyboard mode', () => {
    // The dock behind the scrim keeps its own draft and its own field; flipping
    // the shared flag from here would race the sheet for the caret and outlive
    // the sheet that opened it.
    const openKeyboard = vi.fn();
    const closeKeyboard = vi.fn();
    mockVoiceValue = stubVoice({ openKeyboard, closeKeyboard });
    const { result } = renderHook(() => useTicketKeyboard('t1'));

    act(() => {
      result.current.begin();
    });
    act(() => {
      result.current.end();
    });
    expect(openKeyboard).not.toHaveBeenCalled();
    expect(closeKeyboard).not.toHaveBeenCalled();
  });

  it('posts the draft through the message seam and clears it on success', async () => {
    const submitText = vi.fn(() => Promise.resolve(true));
    mockVoiceValue = stubVoice({ submitText });
    const { result } = renderHook(() => useTicketKeyboard('t1'));

    act(() => {
      result.current.begin();
      result.current.setDraft('  move the button  ');
    });
    act(() => {
      result.current.submit();
    });
    // Trimmed on the way out, and the mode stays open so the next message can be
    // typed straight after.
    expect(submitText).toHaveBeenCalledWith('move the button');
    await waitFor(() => {
      expect(result.current.draft).toBe('');
    });
    expect(result.current.open).toBe(true);
  });

  it('keeps the text when the post fails, so it can be retried', () => {
    const submitText = vi.fn(() => Promise.resolve(false));
    mockVoiceValue = stubVoice({ submitText });
    const { result } = renderHook(() => useTicketKeyboard('t1'));

    act(() => {
      result.current.begin();
      result.current.setDraft('move the button');
    });
    act(() => {
      result.current.submit();
    });
    expect(result.current.draft).toBe('move the button');
  });

  it('refuses to post an empty draft', () => {
    const submitText = vi.fn(() => Promise.resolve(true));
    mockVoiceValue = stubVoice({ submitText });
    const { result } = renderHook(() => useTicketKeyboard('t1'));

    act(() => {
      result.current.begin();
      result.current.setDraft('   ');
    });
    act(() => {
      result.current.submit();
    });
    expect(submitText).not.toHaveBeenCalled();
  });

  it('stands down when the mic goes live — the orb is the way back to voice', () => {
    const { result, rerender } = renderHook(() => useTicketKeyboard('t1'));
    act(() => {
      result.current.begin();
      result.current.setDraft('half a thought');
    });
    expect(result.current.open).toBe(true);

    mockVoiceValue = stubVoice({ micState: 'listening' });
    rerender();
    expect(result.current.open).toBe(false);
    expect(result.current.draft).toBe('');
  });

  it('does not carry a draft to the next ticket, or out of the sheet', () => {
    // The draft is prefixed with the open ticket's title on the way out, so it
    // belongs to that ticket and nothing else.
    const { result, rerender } = renderHook<TicketKeyboard, { id: string | null }>(
      ({ id }) => useTicketKeyboard(id),
      { initialProps: { id: 't1' } },
    );
    act(() => {
      result.current.begin();
      result.current.setDraft('about ticket one');
    });

    rerender({ id: 't2' });
    expect(result.current.open).toBe(false);
    expect(result.current.draft).toBe('');

    act(() => {
      result.current.begin();
      result.current.setDraft('about ticket two');
    });
    rerender({ id: null });
    expect(result.current.open).toBe(false);
    expect(result.current.draft).toBe('');
  });
});
