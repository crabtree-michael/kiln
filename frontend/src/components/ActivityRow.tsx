// The activity row above the dock (08 §4). Pure render of the *current*
// activity state — the store already resolves pill contention (say outranks
// toast; toasts queue; thinking only when the pill is clear), so this component
// only maps the state it is handed to the three selector surfaces the E2E
// asserts: `thinking-indicator`, `toast-pill` (+ `data-verb`), `say-pill`.
import type { JSX } from 'react';
import type { ActivityPill } from '@/stores/activity-context';
import { verbEmoji, verbLabel } from '@/components/feed-format';

export interface ActivityRowProps {
  thinking: boolean;
  pill: ActivityPill;
  /** Dismisses a persistent `say` pill (08 §4). */
  onDismiss: () => void;
}

export function ActivityRow({ thinking, pill, onDismiss }: ActivityRowProps): JSX.Element {
  return (
    <div data-role="activity-row">
      {pill?.kind === 'say' && (
        <div data-role="say-pill">
          <span data-role="say-text">{pill.text}</span>
          <button type="button" data-role="say-dismiss" aria-label="Dismiss" onClick={onDismiss}>
            ×
          </button>
        </div>
      )}

      {pill?.kind === 'toast' && (
        <div data-role="toast-pill" data-verb={pill.verb}>
          <span data-role="toast-icon" aria-hidden="true">
            {verbEmoji(pill.verb)}
          </span>
          <span data-role="toast-text">
            {verbLabel(pill.verb)} <span data-role="toast-title">{pill.ticketTitle}</span>
          </span>
        </div>
      )}

      {pill === null && thinking && (
        <div data-role="thinking-indicator">
          <span data-role="thinking-spinner" aria-hidden="true" />
          <span data-role="thinking-text">Kiln is thinking…</span>
        </div>
      )}
    </div>
  );
}
