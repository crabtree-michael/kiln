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
  enabled: 'Notifications enabled',
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
        <svg data-role="notify-bell" viewBox="0 0 96 96" aria-hidden="true">
          <path
            fill="currentColor"
            d="M48 12 C33 12 25 25 25 43 C25 56 21 63 16.5 67.5 C14.5 69.7 16 74 19.5 74 H76.5 C80 74 81.5 69.7 79.5 67.5 C75 63 71 56 71 43 C71 25 63 12 48 12 Z"
          />
          <path fill="currentColor" d="M39 78 A9 9 0 0 0 57 78 Z" />
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
