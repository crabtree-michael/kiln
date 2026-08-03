// Push-notification setting for the account view (02 §10). One compact row —
// the same inset row shape the GitHub connection uses, since it sits inside the
// settings page's Notifications section: a label and a frequency dropdown.
// The dropdown carries the state, so there is no explanatory copy — the states
// push can't be set from (unsupported / unconfigured / denied) render it inert
// beside a one-word reason, so it still degrades cleanly on a browser or
// deployment without push.
//
// The dropdown spans BOTH halves of the setting, which the old on/off switch
// and the bell menu used to split between them:
//
//   - "Off" is this browser's subscription — selecting it unsubscribes, so the
//     settings page can turn push off on its own, without the bell menu;
//   - the three frequencies are the global mode the bell menu also offers
//     (`useNotificationMode`), and selecting one from a browser that hasn't
//     opted in yet runs the opt-in flow too, since a frequency means nothing
//     without a subscription — the change event is the user gesture
//     `Notification.requestPermission()` requires.
//
// So the selection shown is NOT the stored mode alone: an unsubscribed browser
// reads "Off" whatever mode the account has stored, because that is what this
// browser will actually do.
import type { ChangeEvent, JSX } from 'react';
import { useNotificationMode } from '@/stores/use-notification-mode';
import { useWebPush, type WebPushStatus } from '@/stores/use-web-push';
import type { NotificationModeValue } from '@/transport/transport';

/** The dropdown's off entry — this browser's subscription, not a stored mode,
 * so it is deliberately outside `NotificationModeValue`. */
const OFF = 'off';

/** Why the dropdown is inert, for the states no frequency can apply in. One
 * word each, so the section stays a row; the actionable states (default /
 * enabling / enabled / error) say nothing, because the dropdown itself is the
 * status. "Denied", not "Blocked": "Blocked" is one of the frequencies sitting
 * inches away in the same row, and the two mean different things. */
const REASON: Partial<Record<WebPushStatus, string>> = {
  checking: 'Checking…',
  unsupported: 'Unavailable',
  unconfigured: 'Unavailable',
  denied: 'Denied',
};

interface ModeOption {
  value: NotificationModeValue;
  label: string;
}

/** Ordered least-to-most noisy, matching the bell menu: "Default" (the
 * recommended selection — blocked, completed, started) leads, "All" (every feed
 * update, a testing aid) trails. Labels are single words here because the row
 * has no space for the menu's per-option detail line. "Off" is rendered ahead of
 * these rather than listed among them: it is a different axis (subscribe or not)
 * and only the three below are storable modes. */
const MODE_OPTIONS: readonly ModeOption[] = [
  { value: 'default', label: 'Default' },
  { value: 'blocked', label: 'Blocked' },
  { value: 'all', label: 'All' },
];

/** Narrows a raw option value to the mode union without an assertion — the
 * select only ever offers the three above, so anything else means 'default'.
 * `OFF` never reaches here; the change handler routes it first. */
function toMode(value: string): NotificationModeValue {
  return MODE_OPTIONS.find((option) => option.value === value)?.value ?? 'default';
}

export function NotificationsField(): JSX.Element {
  const { status, error, enable, disable } = useWebPush();
  const { mode, ready, setMode } = useNotificationMode();

  // Settable only where a choice does something: already subscribed, or a fresh
  // or failed state the opt-in can still run from. The pending and terminal
  // states (checking / enabling / unsupported / unconfigured / denied) are inert.
  const actionable = status === 'enabled' || status === 'default' || status === 'error';
  const reason = REASON[status];

  // What this browser will actually do — the stored frequency only once it is
  // subscribed, "Off" otherwise. `enabling` shows the frequency already: the
  // mode write is optimistic, so the row reflects the choice being made rather
  // than snapping back to "Off" for the length of the flow.
  const subscribed = status === 'enabled' || status === 'enabling';
  const selection = subscribed ? mode : OFF;

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
      <select
        data-role="notifications-mode"
        data-status={status}
        aria-labelledby="notifications-title"
        aria-busy={status === 'enabling'}
        disabled={!actionable || !ready}
        value={selection}
        onChange={(event: ChangeEvent<HTMLSelectElement>) => {
          const chosen = event.target.value;
          if (chosen === OFF) {
            // Unsubscribes this browser. The stored mode is left alone — it is
            // the account's frequency, shared with every other browser, and
            // picking a frequency here again is what restores this one.
            void disable();
            return;
          }
          setMode(toMode(chosen));
          if (!subscribed) {
            void enable();
          }
        }}
      >
        <option value={OFF}>Off</option>
        {MODE_OPTIONS.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>

      {error !== null ? (
        <p data-role="notifications-error" role="alert">
          {error}
        </p>
      ) : null}
    </section>
  );
}
