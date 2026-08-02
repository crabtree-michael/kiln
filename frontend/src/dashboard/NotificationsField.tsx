// Push-notification opt-in for the account view (02 §10). One compact row —
// the same inset row shape the GitHub connection uses, since it sits inside the
// settings page's Notifications section: a label and a switch, driven by the
// `useWebPush` hook. The switch carries the state, so there is no explanatory
// copy — the states push can't be turned on from (unsupported / unconfigured /
// denied) render it inert beside a one-word reason, so it still degrades
// cleanly on a browser or deployment without push.
import type { JSX } from 'react';
import { useWebPush, type WebPushStatus } from '@/stores/use-web-push';

/** Why the switch is inert, for the states it can't be flipped in. One word
 * each, so the section stays a row; the actionable states (default / enabling /
 * enabled / error) say nothing, because the switch itself is the status. */
const REASON: Partial<Record<WebPushStatus, string>> = {
  checking: 'Checking…',
  unsupported: 'Unavailable',
  unconfigured: 'Unavailable',
  denied: 'Blocked',
};

export function NotificationsField(): JSX.Element {
  const { status, error, enable, disable } = useWebPush();

  const on = status === 'enabled';
  // Flippable only where a click does something: off → on from a fresh or
  // failed state, on → off to unsubscribe this browser.
  const actionable = on || status === 'default' || status === 'error';
  const reason = REASON[status];

  return (
    <section data-role="notifications-field">
      {/* "Push notifications", not "Notifications": on the settings page this
          field sits under a section already titled Notifications, and naming the
          transport is what actually distinguishes it. */}
      <span id="notifications-title" data-role="notifications-title">
        Push notifications
      </span>
      {reason !== undefined ? (
        <span data-role="notifications-reason" data-status={status}>
          {reason}
        </span>
      ) : null}
      <button
        type="button"
        role="switch"
        data-role="notifications-toggle"
        data-status={status}
        aria-checked={on}
        aria-labelledby="notifications-title"
        aria-busy={status === 'enabling'}
        disabled={!actionable}
        onClick={() => {
          void (on ? disable() : enable());
        }}
      >
        <span data-role="notifications-knob" aria-hidden="true" />
      </button>

      {error !== null ? (
        <p data-role="notifications-error" role="alert">
          {error}
        </p>
      ) : null}
    </section>
  );
}
