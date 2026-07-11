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
import type { ActivityToast } from '@/stores/activity-context';
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
  fetchActivityStatus: vi.fn(),
}));

const TOAST_MS = 20000;

function makeSay(text: string): SayEvent {
  return { message_id: 1, text, at: '2026-07-01T00:00:00Z' };
}

let capturedDismiss: ((id: number) => void) | undefined;
let capturedDismissToast: (() => void) | undefined;
let capturedSetToastExpanded: ((id: number, expanded: boolean) => void) | undefined;
let capturedIds: number[] = [];
let capturedToasts: ActivityToast[] = [];

function Probe(): JSX.Element {
  const { thinking, toasts, dismiss, dismissToast, setToastExpanded } = useActivityStore();
  capturedDismiss = dismiss;
  capturedDismissToast = dismissToast;
  capturedSetToastExpanded = setToastExpanded;
  capturedIds = toasts.map((toast) => toast.id);
  capturedToasts = toasts;
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
    capturedSetToastExpanded = undefined;
    capturedIds = [];
    closeStream.mockClear();
    vi.mocked(transport.openStream).mockImplementation((handlers): StreamConnection => {
      capturedHandlers = handlers;
      return { close: closeStream };
    });
    // Default: the server reports nothing in flight, so the mount/resume/reconnect
    // resync is a no-op unless a test overrides it.
    vi.mocked(transport.fetchActivityStatus).mockResolvedValue({ thinking: false });
  });

  afterEach(() => {
    vi.mocked(transport.openStream).mockReset();
    vi.mocked(transport.fetchActivityStatus).mockReset();
    vi.useRealTimers();
  });

  // Flush the microtasks a resync fetch resolves through, applying its React
  // state update inside act. An empty async act drains the pending promise
  // continuation deterministically even under fake timers.
  async function flushResync(): Promise<void> {
    await act(async () => {
      await Promise.resolve();
    });
  }

  function thinkingAttr(): string | undefined {
    return screen.getByTestId('probe').dataset.thinking;
  }

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
    expect(thinkingAttr()).toBe('true');

    act(() => {
      capturedHandlers?.onActivity?.(makeActivityEvent({ kind: 'thinking', on: false }));
    });
    expect(thinkingAttr()).toBe('false');
  });

  it('resyncs the thinking flag from GET /api/activity on mount', async () => {
    // A pass was already in flight when this client attached; the stream pushes
    // no activity snapshot on connect, so the mount pull is what recovers it.
    vi.mocked(transport.fetchActivityStatus).mockResolvedValue({ thinking: true });
    mount();
    await flushResync();
    expect(thinkingAttr()).toBe('true');
    expect(transport.fetchActivityStatus).toHaveBeenCalled();
  });

  it('recovers a stuck spinner from the server on a reconnecting -> connected transition', async () => {
    mount();
    await flushResync();
    act(() => {
      capturedHandlers?.onActivity?.(makeActivityEvent({ kind: 'thinking', on: true }));
    });
    expect(thinkingAttr()).toBe('true');

    // The stream dropped while Kiln was mid-pass (app backgrounded), so the
    // `thinking off` frame was missed. On reconnect we pull the real state — the
    // pass finished server-side — and the spinner clears.
    vi.mocked(transport.fetchActivityStatus).mockResolvedValue({ thinking: false });
    act(() => {
      capturedHandlers?.onConnectionStateChange('reconnecting');
      capturedHandlers?.onConnectionStateChange('connected');
    });
    await flushResync();
    expect(thinkingAttr()).toBe('false');
  });

  it('keeps the spinner on across reconnect when the server says a pass is still running', async () => {
    mount();
    await flushResync();
    act(() => {
      capturedHandlers?.onActivity?.(makeActivityEvent({ kind: 'thinking', on: true }));
    });

    // Unlike a blind reset-to-false, the pull reflects a genuinely-still-running
    // pass, so the spinner is not wrongly hidden on reconnect.
    vi.mocked(transport.fetchActivityStatus).mockResolvedValue({ thinking: true });
    act(() => {
      capturedHandlers?.onConnectionStateChange('reconnecting');
      capturedHandlers?.onConnectionStateChange('connected');
    });
    await flushResync();
    expect(thinkingAttr()).toBe('true');
  });

  it('resyncs the thinking flag when the app returns to the foreground', async () => {
    mount();
    await flushResync();

    // Backgrounded mid-pass, the closing bracket is missed; the server reports
    // the pass finished, so becoming visible again clears the spinner.
    vi.mocked(transport.fetchActivityStatus).mockResolvedValue({ thinking: false });
    act(() => {
      capturedHandlers?.onActivity?.(makeActivityEvent({ kind: 'thinking', on: true }));
    });
    expect(thinkingAttr()).toBe('true');

    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
    act(() => {
      document.dispatchEvent(new Event('visibilitychange'));
    });
    await flushResync();
    expect(thinkingAttr()).toBe('false');
  });

  it('does not let a stale resync clobber a fresher live thinking frame', async () => {
    mount();
    await flushResync();

    // The reconnect pull resolves to false, but a live `thinking on` frame lands
    // while it is in flight (fresher truth). The generation guard drops the stale
    // pulled snapshot so the live spinner survives.
    let resolvePull: ((s: { thinking: boolean }) => void) | undefined;
    vi.mocked(transport.fetchActivityStatus).mockReturnValue(
      new Promise<{ thinking: boolean }>((resolve) => {
        resolvePull = resolve;
      }),
    );
    act(() => {
      capturedHandlers?.onConnectionStateChange('reconnecting');
      capturedHandlers?.onConnectionStateChange('connected');
    });
    act(() => {
      capturedHandlers?.onActivity?.(makeActivityEvent({ kind: 'thinking', on: true }));
    });
    await act(async () => {
      resolvePull?.({ thinking: false });
      await Promise.resolve();
    });
    expect(thinkingAttr()).toBe('true');
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

  it('carries the wire ticket_id onto the toast pill so a tap can open the ticket', () => {
    mount();
    act(() => {
      capturedHandlers?.onActivity?.(
        makeActivityEvent({
          kind: 'toast',
          verb: 'started',
          ticketTitle: 'Login',
          ticketId: 't-7',
        }),
      );
    });
    const [pill] = capturedToasts.map((toast) => toast.pill);
    expect(pill).toEqual({ kind: 'toast', verb: 'started', ticketTitle: 'Login', ticketId: 't-7' });
  });

  it('pauses a toast while it is expanded so it does not vanish mid-read', () => {
    mount();
    act(() => {
      capturedHandlers?.onActivity?.(
        makeActivityEvent({ kind: 'toast', verb: 'started', ticketTitle: 'Login' }),
      );
    });
    const [id] = capturedIds;

    // The user expands the toast to read a clamped message part-way through its
    // dwell; the auto-dismiss timer is cancelled.
    act(() => {
      vi.advanceTimersByTime(TOAST_MS - 1000);
      capturedSetToastExpanded?.(id!, true);
    });

    // Well past the original 20s window, the expanded toast is still up.
    act(() => {
      vi.advanceTimersByTime(TOAST_MS * 2);
    });
    expect(pills()).toBe('toast:started:Login');
  });

  it('resumes a fresh dwell when a toast is collapsed back down', () => {
    mount();
    act(() => {
      capturedHandlers?.onSay(makeSay('a long utterance'));
    });
    const [id] = capturedIds;

    act(() => {
      vi.advanceTimersByTime(TOAST_MS - 1000);
      capturedSetToastExpanded?.(id!, true);
    });
    // Collapsed again — a full fresh 20s starts from here, not the remaining 1s.
    act(() => {
      capturedSetToastExpanded?.(id!, false);
      vi.advanceTimersByTime(TOAST_MS - 1);
    });
    expect(pills()).toBe('say:a long utterance');

    act(() => {
      vi.advanceTimersByTime(1);
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
