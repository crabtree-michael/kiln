// The one "is this clamped text actually hiding something?" measurement, shared
// by every surface that shows a line-clamped preview and offers to open it: the
// feed card's three-line body (08 §3) and the activity row's two-line toast /
// say pills (08 §4). Both need the same answer — is there more to reveal? — and
// both would otherwise offer a dead affordance on text that already fits.
import { useLayoutEffect, useRef, useState } from 'react';
import type { RefObject } from 'react';

/**
 * Measures whether clamped content actually overflows its clamp. Returns a ref
 * to attach to the clamped element and the `truncated` flag (`scrollHeight`
 * overflows the clamped `clientHeight`, `+1` absorbing sub-pixel rounding).
 * Measured only while `active` (the clamp is applied): an expand-in-place caller
 * passes `active = !expanded` so the flag freezes once the clamp is gone; a
 * caller whose content always clamps passes `true`.
 * jsdom performs no layout, so the flag stays false under test unless the
 * heights are faked. Re-runs when `content` changes (the text) or `active` flips.
 */
export function useClampOverflow<T extends HTMLElement>(
  content: string,
  active: boolean,
): { ref: RefObject<T>; truncated: boolean } {
  const ref = useRef<T>(null);
  const [truncated, setTruncated] = useState(false);

  useLayoutEffect(() => {
    if (!active) return;
    const el = ref.current;
    if (el === null) return;
    const measure = (): void => {
      setTruncated(el.scrollHeight > el.clientHeight + 1);
    };
    measure();
    window.addEventListener('resize', measure);
    return () => {
      window.removeEventListener('resize', measure);
    };
  }, [content, active]);

  return { ref, truncated };
}
