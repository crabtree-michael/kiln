// The activity row above the dock (08 §4). Pure render of the *current*
// activity state — the store owns the notification stack and each entry's timer,
// so this component only maps the toasts it is handed onto the selector surfaces
// the E2E asserts: `thinking-indicator`, `toast-pill` (+ `data-verb`), `say-pill`.
// Multiple live toasts stack into a list; the spinner shows only when the stack
// is empty.
import { useLayoutEffect, useRef, useState } from 'react';
import type { JSX, ReactNode } from 'react';
import type { ActivityToast } from '@/stores/activity-context';
import { verbEmoji, verbLabel } from '@/components/feed-format';

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
          data-role="say-dismiss"
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
      <span data-role="toast-icon" aria-hidden="true">
        {verbEmoji(pill.verb)}
      </span>
      <ClampedText role="toast-text" measureKey={`${pill.verb} ${pill.ticketTitle}`}>
        {verbLabel(pill.verb)} <span data-role="toast-title">{pill.ticketTitle}</span>
      </ClampedText>
    </div>
  );
}

export function ActivityRow({ thinking, toasts, onDismiss }: ActivityRowProps): JSX.Element {
  const empty = toasts.length === 0;

  return (
    <div data-role="activity-row">
      {!empty && (
        <div data-role="toast-stack">
          {toasts.map((toast) => (
            <ActivityToastPill key={toast.id} toast={toast} onDismiss={onDismiss} />
          ))}
        </div>
      )}

      {empty && thinking && (
        <div data-role="thinking-indicator">
          <span data-role="thinking-spinner" aria-hidden="true" />
          <span data-role="thinking-text">Kiln is thinking…</span>
        </div>
      )}
    </div>
  );
}
