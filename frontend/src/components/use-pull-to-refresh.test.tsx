// usePullToRefresh tests: a downward pull from the top of the feed past the
// trigger threshold fires the refresh and holds the spinner up until it settles;
// a short pull springs back without refreshing; an upward move yields to native
// scroll; and an unwired gesture (onRefresh undefined) attaches nothing.
// jsdom ships no TouchEvent, so touch events are synthesized as plain Events with a
// `touches` array bearing the clientX/clientY the hook reads; jsdom performs no
// layout, so the px thresholds are exercised directly off those coordinates.
//
// The direction tests are the ones that keep the feed scrollable both ways: the
// hook must not `preventDefault` — the one-way door that costs the browser its
// scroll — until a touch has proved itself a downward pull.
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

/** A touch event carrying a single point — the only fields the hook reads are
 * clientY and clientX. `cancelable` so `preventDefault` is meaningful for the
 * engaged pull, and observable: the assertions below read `defaultPrevented` to
 * tell "we took the gesture over" from "we left it to native scrolling", which is
 * the whole difference between a feed that scrolls both ways and one that
 * doesn't. */
function touch(type: string, clientY: number, clientX = 0): Event {
  const event = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(event, 'touches', {
    value: clientY < 0 ? [] : [{ clientY, clientX }],
    configurable: true,
  });
  return event;
}

function scroller(): HTMLElement {
  return screen.getByTestId('scroller');
}

/** jsdom has no layout, so the scroll geometry the hook reads (and the scrollTop
 * it writes when a pull is reversed) is stubbed onto the element as plain own
 * properties. */
function stubScrollGeometry(
  el: HTMLElement,
  { scrollTop = 0, clientHeight = 400, scrollHeight = 1000 } = {},
): void {
  Object.defineProperty(el, 'scrollTop', { value: scrollTop, writable: true, configurable: true });
  Object.defineProperty(el, 'clientHeight', { value: clientHeight, configurable: true });
  Object.defineProperty(el, 'scrollHeight', { value: scrollHeight, configurable: true });
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
    const up = touch('touchmove', 70); // dy=-30: hands the gesture back to scroll
    fireEvent(el, up);
    fireEvent(el, touch('touchend', -1));

    expect(up.defaultPrevented).toBe(false);
    expect(onRefresh).not.toHaveBeenCalled();
    expect(screen.getByTestId('pull').textContent).toBe('0');
  });

  it('leaves an upward flick that opens with downward jitter to native scroll', () => {
    // The regression this hook was one-directional over: an upward flick almost
    // always starts with a pixel or two of downward travel, and claiming on that
    // pixel suppressed the scroll for the whole touch. Nothing may be prevented
    // until the finger has committed to a direction.
    const onRefresh = vi.fn(() => Promise.resolve());
    render(<Harness onRefresh={onRefresh} />);
    const el = scroller();

    fireEvent(el, touch('touchstart', 100));
    const jitter = touch('touchmove', 104); // dy=+4, inside the slop
    fireEvent(el, jitter);
    const flick = touch('touchmove', 40); // and away upward
    fireEvent(el, flick);
    fireEvent(el, touch('touchend', -1));

    expect(jitter.defaultPrevented).toBe(false);
    expect(flick.defaultPrevented).toBe(false);
    expect(onRefresh).not.toHaveBeenCalled();
    expect(screen.getByTestId('pull').textContent).toBe('0');
  });

  it('leaves a sideways swipe to the card gesture', () => {
    const onRefresh = vi.fn(() => Promise.resolve());
    render(<Harness onRefresh={onRefresh} />);
    const el = scroller();

    fireEvent(el, touch('touchstart', 100, 200));
    const sideways = touch('touchmove', 112, 320); // more across than down
    fireEvent(el, sideways);
    fireEvent(el, touch('touchend', -1));

    expect(sideways.defaultPrevented).toBe(false);
    expect(onRefresh).not.toHaveBeenCalled();
    expect(screen.getByTestId('pull').textContent).toBe('0');
  });

  it('scrolls the list when a pull is reversed mid-gesture', () => {
    // Once the pull has claimed the touch the browser will not take it back, so
    // reversing has to keep moving the feed — under our own hand.
    const onRefresh = vi.fn(() => Promise.resolve());
    render(<Harness onRefresh={onRefresh} />);
    const el = scroller();
    stubScrollGeometry(el);

    fireEvent(el, touch('touchstart', 100));
    fireEvent(el, touch('touchmove', 160)); // dy=+60 → claimed, pull 30px
    expect(screen.getByTestId('pull').textContent).toBe('30');

    const back = touch('touchmove', 60); // dy=-40, back past the origin
    fireEvent(el, back);
    fireEvent(el, touch('touchend', -1));

    expect(back.defaultPrevented).toBe(true);
    expect(screen.getByTestId('pull').textContent).toBe('0');
    expect(el.scrollTop).toBe(40); // tracked the finger 1:1
    expect(onRefresh).not.toHaveBeenCalled();
  });

  it('never pulls from a drag begun mid-scroll', () => {
    const onRefresh = vi.fn(() => Promise.resolve());
    render(<Harness onRefresh={onRefresh} />);
    const el = scroller();
    stubScrollGeometry(el, { scrollTop: 240 });

    fireEvent(el, touch('touchstart', 100));
    const down = touch('touchmove', 260); // a long downward drag, but not from the top
    fireEvent(el, down);
    fireEvent(el, touch('touchend', -1));

    expect(down.defaultPrevented).toBe(false);
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
