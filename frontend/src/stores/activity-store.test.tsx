// Activity store tests (08 §4): the `thinking` flag, the say/toast contention
// rules, and toast auto-dismiss. Transport is mocked at the module boundary and
// the captured `StreamHandlers` drive live `say`/`activity` events. Fake timers
// exercise the ~4s auto-dismiss deterministically.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import type { JSX } from 'react';
import { ActivityProvider } from '@/stores/activity-store';
import { useActivityStore } from '@/stores/activity-context';
import * as transport from '@/transport/transport';
import type { SayEvent, StreamConnection, StreamHandlers } from '@/transport/transport';
import { makeActivityEvent } from '@/test/fixtures';

vi.mock('@/transport/transport', () => ({
  fetchFeed: vi.fn(),
  postFeedSeen: vi.fn(),
  acceptTicket: vi.fn(),
  fetchBoard: vi.fn(),
  fetchMessages: vi.fn(),
  postMessage: vi.fn(),
  openStream: vi.fn(),
}));

const TOAST_MS = 4000;

function makeSay(text: string): SayEvent {
  return { message_id: 1, text, at: '2026-07-01T00:00:00Z' };
}

let capturedDismiss: (() => void) | undefined;
let capturedDismissToast: (() => void) | undefined;

function Probe(): JSX.Element {
  const { thinking, pill, dismiss, dismissToast } = useActivityStore();
  capturedDismiss = dismiss;
  capturedDismissToast = dismissToast;
  const pillText =
    pill === null
      ? ''
      : pill.kind === 'say'
        ? `say:${pill.text}`
        : `toast:${pill.verb}:${pill.ticketTitle}`;
  return <div data-testid="probe" data-thinking={String(thinking)} data-pill={pillText} />;
}

describe('ActivityProvider', () => {
  let capturedHandlers: StreamHandlers | undefined;
  const closeStream = vi.fn();

  beforeEach(() => {
    vi.useFakeTimers();
    capturedHandlers = undefined;
    capturedDismiss = undefined;
    capturedDismissToast = undefined;
    closeStream.mockClear();
    vi.mocked(transport.openStream).mockImplementation((handlers): StreamConnection => {
      capturedHandlers = handlers;
      return { close: closeStream };
    });
  });

  afterEach(() => {
    vi.mocked(transport.openStream).mockReset();
    vi.useRealTimers();
  });

  function mount(): void {
    render(
      <ActivityProvider>
        <Probe />
      </ActivityProvider>,
    );
  }

  function pill(): string {
    return screen.getByTestId('probe').dataset.pill ?? '';
  }

  it('tracks the thinking flag from activity events', () => {
    mount();
    act(() => {
      capturedHandlers?.onActivity?.(makeActivityEvent({ kind: 'thinking', on: true }));
    });
    expect(screen.getByTestId('probe').dataset.thinking).toBe('true');

    act(() => {
      capturedHandlers?.onActivity?.(makeActivityEvent({ kind: 'thinking', on: false }));
    });
    expect(screen.getByTestId('probe').dataset.thinking).toBe('false');
  });

  it('shows a say pill that persists across time until replaced', () => {
    mount();
    act(() => {
      capturedHandlers?.onSay(makeSay('working on it'));
    });
    expect(pill()).toBe('say:working on it');

    act(() => {
      vi.advanceTimersByTime(TOAST_MS * 3);
    });
    expect(pill()).toBe('say:working on it');
  });

  it('shows a toast and auto-dismisses it after ~4s', () => {
    mount();
    act(() => {
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'started', ticketTitle: 'Login' }),
      );
    });
    expect(pill()).toBe('toast:started:Login');

    act(() => {
      vi.advanceTimersByTime(TOAST_MS);
    });
    expect(pill()).toBe('');
  });

  it('lets a say replace an active toast', () => {
    mount();
    act(() => {
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'started', ticketTitle: 'Login' }),
      );
    });
    expect(pill()).toBe('toast:started:Login');

    act(() => {
      capturedHandlers?.onSay(makeSay('reply'));
    });
    expect(pill()).toBe('say:reply');
  });

  it('queues toasts behind an active say, then drains them one at a time on dismiss', () => {
    mount();
    act(() => {
      capturedHandlers?.onSay(makeSay('hold'));
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'finished', ticketTitle: 'A' }),
      );
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'queued', ticketTitle: 'B' }),
      );
    });
    // The say stays; both toasts are queued behind it, not shown.
    expect(pill()).toBe('say:hold');

    act(() => {
      capturedDismiss?.();
    });
    expect(pill()).toBe('toast:finished:A');

    act(() => {
      vi.advanceTimersByTime(TOAST_MS);
    });
    expect(pill()).toBe('toast:queued:B');

    act(() => {
      vi.advanceTimersByTime(TOAST_MS);
    });
    expect(pill()).toBe('');
  });

  it('dismissToast clears a showing toast and drains the queue (input sent)', () => {
    mount();
    act(() => {
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'started', ticketTitle: 'A' }),
      );
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'nudged', ticketTitle: 'B' }),
      );
    });
    expect(pill()).toBe('toast:started:A');

    // Submitting mid-toast dismisses the current one; the queued toast then
    // takes its normal turn (new toasts are unaffected).
    act(() => {
      capturedDismissToast?.();
    });
    expect(pill()).toBe('toast:nudged:B');

    act(() => {
      vi.advanceTimersByTime(TOAST_MS);
    });
    expect(pill()).toBe('');
  });

  it('dismissToast leaves a persistent say untouched', () => {
    mount();
    act(() => {
      capturedHandlers?.onSay(makeSay('reply'));
    });
    expect(pill()).toBe('say:reply');

    act(() => {
      capturedDismissToast?.();
    });
    expect(pill()).toBe('say:reply');
  });

  it('dismissToast is a no-op when the row is already clear', () => {
    mount();
    expect(pill()).toBe('');

    act(() => {
      capturedDismissToast?.();
    });
    expect(pill()).toBe('');
  });

  it('queues a second toast behind an active toast', () => {
    mount();
    act(() => {
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'started', ticketTitle: 'A' }),
      );
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'nudged', ticketTitle: 'B' }),
      );
    });
    expect(pill()).toBe('toast:started:A');

    act(() => {
      vi.advanceTimersByTime(TOAST_MS);
    });
    expect(pill()).toBe('toast:nudged:B');

    act(() => {
      vi.advanceTimersByTime(TOAST_MS);
    });
    expect(pill()).toBe('');
  });
});
