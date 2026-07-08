// usePullToRefresh tests (this change): a downward pull from the top of the feed
// past the trigger threshold fires the refresh and holds the spinner up until it
// settles; a short pull springs back without refreshing; an upward move yields to
// native scroll; and an unwired gesture (onRefresh undefined) attaches nothing.
// jsdom ships no TouchEvent, so touch events are synthesized as plain Events with a
// `touches` array bearing the clientY the hook reads; jsdom performs no layout, so
// the px thresholds are exercised directly off those coordinates.
import { useRef, type JSX } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { usePullToRefresh } from '@/components/use-pull-to-refresh';

function Harness({ onRefresh }: { onRefresh?: (() => Promise<void>) | undefined }): JSX.Element {
  const ref = useRef<HTMLDivElement>(null);
  const { pull, refreshing, dragging } = usePullToRefresh(ref, onRefresh);
  return (
    <div ref={ref} data-testid="scroller">
      <span data-testid="pull">{String(pull)}</span>
      <span data-testid="refreshing">{String(refreshing)}</span>
      <span data-testid="dragging">{String(dragging)}</span>
    </div>
  );
}

/** A touch event carrying a single point at `clientY` — the only field the hook
 * reads. `cancelable` so `preventDefault` is meaningful for the engaged pull. */
function touch(type: string, clientY: number): Event {
  const event = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(event, 'touches', {
    value: clientY < 0 ? [] : [{ clientY }],
    configurable: true,
  });
  return event;
}

function scroller(): HTMLElement {
  return screen.getByTestId('scroller');
}

describe('usePullToRefresh', () => {
  it('refreshes on a pull past the threshold and holds the spinner until it settles', async () => {
    let resolveRefresh = (): void => {
      // Replaced with the promise's resolve below; never called as-is.
    };
    const onRefresh = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveRefresh = resolve;
        }),
    );
    render(<Harness onRefresh={onRefresh} />);
    const el = scroller();

    fireEvent(el, touch('touchstart', 100));
    fireEvent(el, touch('touchmove', 220)); // dy=120 → pull 60px, past the 56px trigger
    expect(screen.getByTestId('pull').textContent).toBe('60');
    expect(screen.getByTestId('dragging').textContent).toBe('true');

    fireEvent(el, touch('touchend', -1));
    // Refresh fired and the indicator is held open (spinning) for the round-trip.
    expect(onRefresh).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('refreshing').textContent).toBe('true');
    expect(screen.getByTestId('pull').textContent).toBe('44');

    // Even after the fetch resolves the spinner lingers (min visible time), then
    // springs back to rest.
    resolveRefresh();
    await waitFor(() => {
      expect(screen.getByTestId('refreshing').textContent).toBe('false');
    });
    expect(screen.getByTestId('pull').textContent).toBe('0');
  });

  it('springs back without refreshing on a short pull', () => {
    const onRefresh = vi.fn(() => Promise.resolve());
    render(<Harness onRefresh={onRefresh} />);
    const el = scroller();

    fireEvent(el, touch('touchstart', 100));
    fireEvent(el, touch('touchmove', 140)); // dy=40 → pull 20px, under the trigger
    fireEvent(el, touch('touchend', -1));

    expect(onRefresh).not.toHaveBeenCalled();
    expect(screen.getByTestId('pull').textContent).toBe('0');
  });

  it('yields to native scroll on an upward move and never refreshes', () => {
    const onRefresh = vi.fn(() => Promise.resolve());
    render(<Harness onRefresh={onRefresh} />);
    const el = scroller();

    fireEvent(el, touch('touchstart', 100));
    fireEvent(el, touch('touchmove', 70)); // dy=-30: hands the gesture back to scroll
    fireEvent(el, touch('touchend', -1));

    expect(onRefresh).not.toHaveBeenCalled();
    expect(screen.getByTestId('pull').textContent).toBe('0');
  });

  it('attaches nothing when the gesture is unwired', () => {
    render(<Harness onRefresh={undefined} />);
    const el = scroller();

    fireEvent(el, touch('touchstart', 100));
    fireEvent(el, touch('touchmove', 260));
    fireEvent(el, touch('touchend', -1));

    expect(screen.getByTestId('pull').textContent).toBe('0');
    expect(screen.getByTestId('refreshing').textContent).toBe('false');
  });
});
