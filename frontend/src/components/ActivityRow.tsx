// The activity row above the dock (08 §4). Pure render of the *current*
// activity state — the store owns the notification stack and each entry's timer,
// so this component only maps the toasts it is handed onto the selector surfaces
// the E2E asserts: `thinking-indicator`, `toast-pill` (+ `data-verb`), `say-pill`.
// Multiple live toasts stack into a list; the spinner shows only when the stack
// is empty.
import type { JSX } from 'react';
import type { ActivityToast } from '@/stores/activity-context';
import { verbEmoji, verbLabel } from '@/components/feed-format';

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
        <span data-role="say-text">{pill.text}</span>
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
      <span data-role="toast-text">
        {verbLabel(pill.verb)} <span data-role="toast-title">{pill.ticketTitle}</span>
      </span>
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
