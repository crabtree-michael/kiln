// SwipeToDismiss tests (08 §3): a leftward drag past the threshold clears the row
// (fires onDismiss after the fling), a short drag springs back, and a mostly-
// vertical drag yields to scroll (never clears). Pointer events are synthesized
// with explicit coordinates; jsdom performs no layout, so the px thresholds in
// the component are exercised directly off the client coordinates.
import { beforeAll, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { SwipeToDismiss } from '@/components/SwipeToDismiss';

// jsdom ships no PointerEvent, so testing-library's fireEvent.pointer* would drop
// the clientX/clientY the gesture reads. Back it with a MouseEvent (which jsdom
// does carry coordinates for), adding the pointer fields the component touches.
class StubPointerEvent extends MouseEvent {
  readonly pointerId: number;
  readonly pointerType: string;
  constructor(type: string, params: PointerEventInit = {}) {
    super(type, params);
    this.pointerId = params.pointerId ?? 0;
    this.pointerType = params.pointerType ?? '';
  }
}

beforeAll(() => {
  vi.stubGlobal('PointerEvent', StubPointerEvent);
});

function content(): HTMLElement {
  const el = document.querySelector('[data-role="swipe-content"]');
  if (!(el instanceof HTMLElement)) {
    throw new Error('swipe-content not found');
  }
  return el;
}

function pointer(clientX: number, clientY: number): PointerEventInit {
  return { clientX, clientY, pointerId: 1, pointerType: 'touch', button: 0 };
}

describe('SwipeToDismiss', () => {
  it('clears the row when swiped left past the threshold', async () => {
    const onDismiss = vi.fn();
    render(
      <SwipeToDismiss onDismiss={onDismiss}>
        <div>card</div>
      </SwipeToDismiss>,
    );
    const el = content();

    fireEvent.pointerDown(el, pointer(200, 100));
    fireEvent.pointerMove(el, pointer(120, 104)); // clearly horizontal, engages
    fireEvent.pointerMove(el, pointer(100, 104)); // 100px left, past threshold
    fireEvent.pointerUp(el, pointer(100, 104));

    await waitFor(() => {
      expect(onDismiss).toHaveBeenCalledTimes(1);
    });
  });

  it('springs back without clearing on a short swipe', async () => {
    const onDismiss = vi.fn();
    render(
      <SwipeToDismiss onDismiss={onDismiss}>
        <div>card</div>
      </SwipeToDismiss>,
    );
    const el = content();

    fireEvent.pointerDown(el, pointer(200, 100));
    fireEvent.pointerMove(el, pointer(180, 102)); // ~20px left, under threshold
    fireEvent.pointerUp(el, pointer(180, 102));

    // Give any (non-existent) fling timer a chance to fire.
    await new Promise((resolve) => setTimeout(resolve, 300));
    expect(onDismiss).not.toHaveBeenCalled();
    // Content is back at rest (no lingering transform offset).
    expect(el.style.transform).toBe('translateX(0px)');
  });

  it('yields to vertical scroll and never clears on a mostly-vertical drag', async () => {
    const onDismiss = vi.fn();
    render(
      <SwipeToDismiss onDismiss={onDismiss}>
        <div>card</div>
      </SwipeToDismiss>,
    );
    const el = content();

    fireEvent.pointerDown(el, pointer(200, 100));
    fireEvent.pointerMove(el, pointer(190, 200)); // dy >> dx: vertical intent
    fireEvent.pointerMove(el, pointer(120, 260)); // even a later leftward move is ignored
    fireEvent.pointerUp(el, pointer(120, 260));

    await new Promise((resolve) => setTimeout(resolve, 300));
    expect(onDismiss).not.toHaveBeenCalled();
  });

  it('renders its child row', () => {
    render(
      <SwipeToDismiss onDismiss={vi.fn()}>
        <div>hello card</div>
      </SwipeToDismiss>,
    );
    expect(screen.getByText('hello card')).toBeInTheDocument();
  });
});
