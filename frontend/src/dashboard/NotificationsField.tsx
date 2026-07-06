// Push-notification opt-in for the account view (02 §10). A single "Enable
// notifications" control that reflects the browser + backend capability and the
// current subscription state, driven by the `useWebPush` hook. It renders
// nothing actionable when notifications are unsupported or unconfigured — just
// an explanation — so it degrades cleanly on a browser or deployment without
// push.
import type { JSX } from 'react';
import { useWebPush, type WebPushStatus } from '@/stores/use-web-push';

/** A short label for the status chip. */
const CHIP_LABEL: Record<WebPushStatus, string> = {
  checking: 'Checking…',
  unsupported: 'Unavailable',
  unconfigured: 'Unavailable',
  default: 'Off',
  denied: 'Blocked',
  enabling: 'Working…',
  enabled: 'On',
  error: 'Off',
};

/** The one-line explanation shown beneath the chip, when a state warrants one. */
const NOTE: Partial<Record<WebPushStatus, string>> = {
  unsupported: 'This browser doesn’t support push notifications.',
  unconfigured: 'Push notifications aren’t configured on the server.',
  denied: 'Notifications are blocked — allow them in your browser settings, then reload.',
  enabled: 'Notifications are on for this device.',
};

export function NotificationsField(): JSX.Element {
  const { status, error, enable } = useWebPush();

  // A button is offered only when the user can start or retry the flow.
  const canEnable = status === 'default' || status === 'error';
  const enabling = status === 'enabling';
  const note = NOTE[status];

  return (
    <section data-role="notifications-field">
      <div data-role="notifications-header">
        <span data-role="notifications-title">Notifications</span>
        <span data-role="notifications-status" data-status={status}>
          {CHIP_LABEL[status]}
        </span>
      </div>

      {canEnable || enabling ? (
        <button
          type="button"
          data-role="notifications-enable"
          disabled={enabling}
          onClick={() => {
            void enable();
          }}
        >
          {enabling ? 'Enabling…' : 'Enable notifications'}
        </button>
      ) : null}

      {note !== undefined ? <p data-role="notifications-note">{note}</p> : null}

      {error !== null ? (
        <p data-role="notifications-error" role="alert">
          {error}
        </p>
      ) : null}
    </section>
  );
}
