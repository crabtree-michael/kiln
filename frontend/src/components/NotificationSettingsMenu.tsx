// The bell notification-settings dropdown in the top nav, left of the stream
// status menu (02 §10). Presentational: the current mode + push capability come
// in as props (PrimaryScreen bridges the stores), open/close is local UI state.
// It mirrors HeaderStatusMenu's dropdown mechanics (click-outside / Escape to
// dismiss, panel stays mounted so it animates both ways).
//
// Two selectable frequencies — "All updates" (a push on every feed update, a
// testing aid) and "Blocked" (the default: a push only when a ticket needs a
// human decision) — plus a button that requests OS notification permission and
// registers the browser for push, for verifying delivery is wired up. The menu
// is deliberately simple; more modes may be added later.
import { useEffect, useRef, useState, type JSX } from 'react';
import type { NotificationModeValue } from '@/transport/transport';
import type { WebPushStatus } from '@/stores/use-web-push';

export interface NotificationSettingsMenuProps {
  /** The current frequency; drives which option reads as selected. */
  mode: NotificationModeValue;
  /** Persist a new frequency. Optional so presentational tests can omit it. */
  onSelectMode?: ((mode: NotificationModeValue) => void) | undefined;
  /** The browser + backend push capability, for the permission button's label
   * and enabled state. Optional; omitted renders the button as "checking". */
  pushStatus?: WebPushStatus | undefined;
  /** Request OS notification permission + register for push. Optional. */
  onEnablePush?: (() => void) | undefined;
}

interface ModeOption {
  value: NotificationModeValue;
  label: string;
  detail: string;
}

// Order intentionally leads with "All updates" — the frequency a tester reaches
// for — but "Blocked" is the default selection unless the user changes it.
const MODE_OPTIONS: ModeOption[] = [
  { value: 'all', label: 'All updates', detail: 'Notify on every feed update.' },
  { value: 'blocked', label: 'Blocked', detail: 'Notify only when a ticket needs you.' },
];

// The permission button's label per push state. `default`/`error` invite the
// action; the terminal states explain why it is unavailable or already done.
const PUSH_LABEL: Record<WebPushStatus, string> = {
  checking: 'Checking notifications…',
  unsupported: 'Notifications unavailable',
  unconfigured: 'Notifications not configured',
  default: 'Enable notifications',
  denied: 'Notifications blocked in browser',
  enabling: 'Enabling…',
  enabled: 'Notifications enabled — send test prompt',
  error: 'Retry enabling notifications',
};

export function NotificationSettingsMenu({
  mode,
  onSelectMode,
  pushStatus = 'checking',
  onEnablePush,
}: NotificationSettingsMenuProps): JSX.Element {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  // While open, a click anywhere outside — or Escape — dismisses it (mirrors
  // HeaderStatusMenu).
  useEffect(() => {
    if (!open) {
      return;
    }
    function onPointerDown(event: MouseEvent): void {
      const target = event.target;
      if (target instanceof Node && rootRef.current !== null && !rootRef.current.contains(target)) {
        setOpen(false);
      }
    }
    function onKeyDown(event: KeyboardEvent): void {
      if (event.key === 'Escape') {
        setOpen(false);
      }
    }
    document.addEventListener('mousedown', onPointerDown);
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [open]);

  // The button is actionable only when the user can start or retry the flow;
  // the terminal/pending states render it disabled as a status line.
  const canEnablePush =
    pushStatus === 'default' || pushStatus === 'error' || pushStatus === 'enabled';

  return (
    <div data-role="notify-settings" ref={rootRef}>
      <button
        type="button"
        data-role="notify-settings-trigger"
        data-open={open}
        aria-haspopup="true"
        aria-expanded={open}
        aria-controls="notify-settings-panel"
        aria-label="Notification settings"
        onClick={() => {
          setOpen((wasOpen) => !wasOpen);
        }}
      >
        <svg data-role="notify-bell" viewBox="0 0 20 20" aria-hidden="true">
          <path
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
            d="M10 3a4 4 0 0 0-4 4c0 3-1.2 4.6-1.8 5.3-.3.4 0 1 .5 1h10.6c.5 0 .8-.6.5-1C15.2 11.6 14 10 14 7a4 4 0 0 0-4-4ZM8.5 16a1.5 1.5 0 0 0 3 0"
          />
        </svg>
      </button>
      <div
        id="notify-settings-panel"
        data-role="notify-settings-panel"
        data-open={open}
        aria-hidden={!open}
      >
        <div data-role="notify-settings-heading">Notifications</div>
        <ul data-role="notify-settings-list">
          {MODE_OPTIONS.map((option) => {
            const selected = option.value === mode;
            return (
              <li key={option.value}>
                <button
                  type="button"
                  data-role="notify-settings-option"
                  data-value={option.value}
                  data-selected={selected}
                  aria-pressed={selected}
                  disabled={onSelectMode === undefined}
                  onClick={() => {
                    onSelectMode?.(option.value);
                    setOpen(false);
                  }}
                >
                  <span data-role="notify-option-check" aria-hidden="true" />
                  <span data-role="notify-option-text">
                    <span data-role="notify-option-label">{option.label}</span>
                    <span data-role="notify-option-detail">{option.detail}</span>
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
        <button
          type="button"
          data-role="notify-settings-permission"
          data-status={pushStatus}
          disabled={!canEnablePush || onEnablePush === undefined}
          onClick={() => {
            onEnablePush?.();
          }}
        >
          {PUSH_LABEL[pushStatus]}
        </button>
      </div>
    </div>
  );
}
