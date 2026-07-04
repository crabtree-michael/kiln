// Agent notifications panel for the debug view. Surfaces the brain-authored
// notification rows (the `update`/`preview` feed cards — see 08 §3, "update and
// preview are brain-authored notification rows") that the primary screen folds
// into its backlog. Here they get their own panel alongside the board and chat
// so a developer can watch agent notifications land in isolation. Presentational
// only: it takes the already-filtered cards and never touches the transport or
// stores directly (the composing wrapper in `App.tsx` bridges the feed store).
import type { JSX } from 'react';
import type { FeedCard } from '@/transport/transport';
import { cardTag, relativeAge } from '@/components/feed-format';

export interface NotificationsPanelProps {
  /** The notification-backed cards (update/preview), newest first. */
  notifications: FeedCard[];
  /** Injected "now" for deterministic relative-age rendering (defaults to real time). */
  now?: number;
}

export function NotificationsPanel({
  notifications,
  now = Date.now(),
}: NotificationsPanelProps): JSX.Element {
  return (
    <section aria-label="Notifications" data-role="notifications-panel">
      {notifications.length === 0 ? (
        <p data-role="notifications-empty">No agent notifications yet.</p>
      ) : (
        <ul data-role="notifications-list">
          {notifications.map((card) => (
            <li key={card.id} data-role="notification-item" data-kind={card.kind}>
              <div data-role="notification-head">
                <span data-role="notification-label">{card.label}</span>
                <span data-role="notification-tag">{cardTag(card.kind)}</span>
                <span data-role="notification-age">{relativeAge(card.created_at, now)}</span>
              </div>
              <p data-role="notification-body">{card.body}</p>
              {card.kind === 'preview' && card.image_url != null && (
                <img data-role="notification-image" src={card.image_url} alt={card.label} />
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
