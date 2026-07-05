// Activity store tests (08 §4): the `thinking` flag and the notification stack —
// every source (say + toast) pushes onto one stack rather than overwriting, and
// each entry auto-dismisses on its own 20s clock. Transport is mocked at the
// module boundary and the captured `StreamHandlers` drive live `say`/`activity`
// events. Fake timers exercise the independent auto-dismiss deterministically.
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

const TOAST_MS = 20000;

function makeSay(text: string): SayEvent {
  return { message_id: 1, text, at: '2026-07-01T00:00:00Z' };
}

let capturedDismiss: ((id: number) => void) | undefined;
let capturedDismissToast: (() => void) | undefined;
let capturedIds: number[] = [];

function Probe(): JSX.Element {
  const { thinking, toasts, dismiss, dismissToast } = useActivityStore();
  capturedDismiss = dismiss;
  capturedDismissToast = dismissToast;
  capturedIds = toasts.map((toast) => toast.id);
  const rendered = toasts
    .map((toast) =>
      toast.pill.kind === 'say'
        ? `say:${toast.pill.text}`
        : `toast:${toast.pill.verb}:${toast.pill.ticketTitle}`,
    )
    .join('|');
  return <div data-testid="probe" data-thinking={String(thinking)} data-pills={rendered} />;
}

describe('ActivityProvider', () => {
  let capturedHandlers: StreamHandlers | undefined;
  const closeStream = vi.fn();

  beforeEach(() => {
    vi.useFakeTimers();
    capturedHandlers = undefined;
    capturedDismiss = undefined;
    capturedDismissToast = undefined;
    capturedIds = [];
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

  function pills(): string {
    return screen.getByTestId('probe').dataset.pills ?? '';
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

  it('shows a say pill and auto-dismisses it after 20s', () => {
    mount();
    act(() => {
      capturedHandlers?.onSay(makeSay('working on it'));
    });
    expect(pills()).toBe('say:working on it');

    act(() => {
      vi.advanceTimersByTime(TOAST_MS - 1);
    });
    expect(pills()).toBe('say:working on it');

    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(pills()).toBe('');
  });

  it('shows a toast and auto-dismisses it after 20s', () => {
    mount();
    act(() => {
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'started', ticketTitle: 'Login' }),
      );
    });
    expect(pills()).toBe('toast:started:Login');

    act(() => {
      vi.advanceTimersByTime(TOAST_MS);
    });
    expect(pills()).toBe('');
  });

  it('stacks a say and a toast together rather than overwriting', () => {
    mount();
    act(() => {
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'started', ticketTitle: 'Login' }),
      );
      capturedHandlers?.onSay(makeSay('reply'));
    });
    // Both are live at once, in arrival order — nothing is overwritten.
    expect(pills()).toBe('toast:started:Login|say:reply');
  });

  it('stacks several toasts fired in quick succession', () => {
    mount();
    act(() => {
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'started', ticketTitle: 'A' }),
      );
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'nudged', ticketTitle: 'B' }),
      );
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'finished', ticketTitle: 'C' }),
      );
    });
    expect(pills()).toBe('toast:started:A|toast:nudged:B|toast:finished:C');
  });

  it('dismisses each stacked toast independently on its own 20s clock', () => {
    mount();
    act(() => {
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'started', ticketTitle: 'A' }),
      );
    });
    // B arrives 5s after A, so their timers are offset.
    act(() => {
      vi.advanceTimersByTime(5000);
    });
    act(() => {
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'nudged', ticketTitle: 'B' }),
      );
    });
    expect(pills()).toBe('toast:started:A|toast:nudged:B');

    // A expires 20s after it arrived; B outlives it and the stack reflows.
    act(() => {
      vi.advanceTimersByTime(15000);
    });
    expect(pills()).toBe('toast:nudged:B');

    // B expires 20s after its own arrival.
    act(() => {
      vi.advanceTimersByTime(5000);
    });
    expect(pills()).toBe('');
  });

  it('lets a say be dismissed early by id without disturbing other toasts', () => {
    mount();
    act(() => {
      capturedHandlers?.onSay(makeSay('hold'));
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'finished', ticketTitle: 'A' }),
      );
    });
    expect(pills()).toBe('say:hold|toast:finished:A');

    // Dismiss just the say (first id); the toast stays until its own timer.
    const sayId = capturedIds[0];
    if (sayId === undefined) {
      throw new Error('expected a say toast in the stack');
    }
    act(() => {
      capturedDismiss?.(sayId);
    });
    expect(pills()).toBe('toast:finished:A');

    act(() => {
      vi.advanceTimersByTime(TOAST_MS);
    });
    expect(pills()).toBe('');
  });

  it('dismissToast clears every live toast at once when input is sent', () => {
    mount();
    act(() => {
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'started', ticketTitle: 'A' }),
      );
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'nudged', ticketTitle: 'B' }),
      );
    });
    expect(pills()).toBe('toast:started:A|toast:nudged:B');

    // Sending input supersedes lingering board toasts: the whole transient
    // stack clears at once.
    act(() => {
      capturedDismissToast?.();
    });
    expect(pills()).toBe('');

    // The cleared toasts' timers are gone (no stale expiry), and toasts raised
    // after submission still behave normally.
    act(() => {
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'finished', ticketTitle: 'C' }),
      );
    });
    expect(pills()).toBe('toast:finished:C');
    act(() => {
      vi.advanceTimersByTime(TOAST_MS);
    });
    expect(pills()).toBe('');
  });

  it('dismissToast clears transient toasts but leaves a coexisting say', () => {
    mount();
    act(() => {
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'started', ticketTitle: 'A' }),
      );
      capturedHandlers?.onSay(makeSay('reply'));
    });
    expect(pills()).toBe('toast:started:A|say:reply');

    // The transient toast goes; the persistent say is left for its own dismiss.
    act(() => {
      capturedDismissToast?.();
    });
    expect(pills()).toBe('say:reply');
  });

  it('dismissToast is a no-op when the row is already clear', () => {
    mount();
    expect(pills()).toBe('');

    act(() => {
      capturedDismissToast?.();
    });
    expect(pills()).toBe('');
  });
});
