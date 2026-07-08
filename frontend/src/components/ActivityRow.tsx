// The activity row above the dock (08 §4). Pure render of the *current*
// activity state — the store owns the notification stack and each entry's timer,
// so this component only maps the toasts it is handed onto the selector surfaces
// the E2E asserts: `thinking-indicator`, `toast-pill` (+ `data-verb`), `say-pill`.
// Multiple live toasts stack into a list; the spinner shows only when the stack
// is empty.
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { JSX, ReactNode } from 'react';
import type { ActivityToast } from '@/stores/activity-context';
import { verbEmoji, verbLabel } from '@/components/feed-format';
import { pickKilnWord } from '@/components/kiln-words';

/**
 * The clamped text inside a toast/say pill. Mobile caps the message at 2 lines
 * (df0f2a75), which silently hid the tail of longer messages — including agent
 * `say` output. When the clamp actually bites we turn the text into a tappable
 * button that reveals the full message in place (`data-expanded`); text that
 * fits stays inert and renders exactly as before.
 *
 * Truncation is measured (`scrollHeight` overflows the clamped `clientHeight`)
 * only while collapsed — once expanded the clamp is gone and the two heights
 * agree, so we freeze the flag rather than re-measuring. `measureKey` re-runs
 * the check when the message text changes.
 */
function ClampedText({
  role,
  measureKey,
  children,
}: {
  role: 'say-text' | 'toast-text';
  measureKey: string;
  children: ReactNode;
}): JSX.Element {
  const ref = useRef<HTMLSpanElement>(null);
  const [truncated, setTruncated] = useState(false);
  const [expanded, setExpanded] = useState(false);

  useLayoutEffect(() => {
    // Only meaningful against the clamped box; skip while expanded so the
    // frozen `truncated` flag keeps the collapse affordance available.
    if (expanded) return;
    const el = ref.current;
    if (el === null) return;
    // `+1` absorbs sub-pixel rounding between scroll/client height.
    const measure = (): void => {
      setTruncated(el.scrollHeight > el.clientHeight + 1);
    };
    measure();
    window.addEventListener('resize', measure);
    return () => {
      window.removeEventListener('resize', measure);
    };
  }, [measureKey, expanded]);

  const interactive = truncated || expanded;
  const toggle = (): void => {
    setExpanded((value) => !value);
  };

  return (
    <span
      ref={ref}
      data-role={role}
      data-expandable={interactive ? 'true' : undefined}
      data-expanded={expanded ? 'true' : undefined}
      role={interactive ? 'button' : undefined}
      tabIndex={interactive ? 0 : undefined}
      aria-expanded={interactive ? expanded : undefined}
      onClick={interactive ? toggle : undefined}
      onKeyDown={
        interactive
          ? (event) => {
              if (event.key === 'Enter' || event.key === ' ') {
                event.preventDefault();
                toggle();
              }
            }
          : undefined
      }
    >
      {children}
    </span>
  );
}

export interface ActivityRowProps {
  thinking: boolean;
  toasts: ActivityToast[];
  /** Dismisses one toast by id (e.g. a persistent `say`) (08 §4). */
  onDismiss: (id: number) => void;
}

function ActivityToastPill({
  toast,
  onDismiss,
}: {
  toast: ActivityToast;
  onDismiss: (id: number) => void;
}): JSX.Element | null {
  const { id, pill } = toast;

  if (pill.kind === 'say') {
    return (
      <div data-role="say-pill">
        <ClampedText role="say-text" measureKey={pill.text}>
          {pill.text}
        </ClampedText>
        <button
          type="button"
          data-role="toast-dismiss"
          aria-label="Dismiss"
          onClick={() => {
            onDismiss(id);
          }}
        >
          ×
        </button>
      </div>
    );
  }

  return (
    <div data-role="toast-pill" data-verb={pill.verb}>
      <span data-role="toast-icon" role="img" aria-label={verbLabel(pill.verb)}>
        {verbEmoji(pill.verb)}
      </span>
      <ClampedText role="toast-text" measureKey={pill.ticketTitle}>
        <span data-role="toast-title">{pill.ticketTitle}</span>
      </ClampedText>
      <button
        type="button"
        data-role="toast-dismiss"
        aria-label="Dismiss"
        onClick={() => {
          onDismiss(id);
        }}
      >
        ×
      </button>
    </div>
  );
}

export function ActivityRow({ thinking, toasts, onDismiss }: ActivityRowProps): JSX.Element {
  const empty = toasts.length === 0;
  const rowRef = useRef<HTMLDivElement>(null);

  // Swap the static "thinking" for a random clay-work verb (sculpting, molding,
  // firing…) so the pill stays on-brand. Re-rolled each time the pill appears
  // (`thinking` flips true) and held steady while it stays up, so the word
  // doesn't flicker on unrelated re-renders. `thinking` is the intended re-roll
  // trigger even though the callback doesn't read it.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const word = useMemo(() => pickKilnWord(), [thinking]);

  // Keep the feed's last card clear of this band. The activity row is an
  // out-of-flow overlay anchored above the dock (PrimaryScreen.css): when it
  // holds a "Kiln is thinking…" spinner or a toast stack it floats UP over the
  // feed's bottom with an opaque fill, occluding the newest card(s) — and with
  // nothing reserving that space the feed can't be scrolled far enough to reveal
  // them. Mirror the live transcript's `--dock-overlay-height` trick: publish the
  // band's current height as `--feed-bottom-inset` on the screen root so the feed
  // adds exactly that much bottom scroll inset (0px when the band is empty, so the
  // idle layout is untouched), tracked live via ResizeObserver as toasts stack /
  // dismiss and the spinner comes and goes. Written on the screen root (not this
  // row) so it reaches the feed, a distant sibling; a no-op when the row renders
  // outside a primary screen (isolated tests) since `closest` is null.
  useEffect(() => {
    const el = rowRef.current;
    const root = el?.closest<HTMLElement>('[data-role="primary-screen"]') ?? null;
    if (root === null) {
      return;
    }
    const publish = (): void => {
      root.style.setProperty('--feed-bottom-inset', `${(el?.offsetHeight ?? 0).toString()}px`);
    };
    publish();
    if (el === null || typeof ResizeObserver === 'undefined') {
      return () => {
        root.style.removeProperty('--feed-bottom-inset');
      };
    }
    const observer = new ResizeObserver(publish);
    observer.observe(el);
    return () => {
      observer.disconnect();
      root.style.removeProperty('--feed-bottom-inset');
    };
  }, []);

  return (
    <div data-role="activity-row" ref={rowRef}>
      {/* The "Kiln is thinking…" pill renders FIRST — at the top of the flex
          column, farthest from the dock — so it floats clear above any toasts.
          It carries its own opaque background and ember glow (PrimaryScreen.css),
          so unlike the toast stack it does not sit on the dock's page-tone band;
          it reads as an elevated chip hovering over the page. The toast stack
          follows below, nearest the dock, where its band merges with the dock as
          one continuous surface. */}
      {thinking && (
        <div data-role="thinking-indicator">
          <span data-role="thinking-text">Kiln is {word}…</span>
        </div>
      )}

      {!empty && (
        <div data-role="toast-stack">
          {toasts.map((toast) => (
            <ActivityToastPill key={toast.id} toast={toast} onDismiss={onDismiss} />
          ))}
        </div>
      )}
    </div>
  );
}
