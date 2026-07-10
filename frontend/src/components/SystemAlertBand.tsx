// The permanent error band above the dock. Unlike an activity toast — which is
// ephemeral and auto-dismisses (08 §4) — this is a persistent state section: it
// stays as long as the server reports a `SystemAlert` on the board snapshot and
// vanishes the moment the alert drops out (the underlying condition recovered).
//
// Deliberately error-agnostic: it renders whatever `detail` sentence the server
// sends and never interprets `kind`, so the same section works for any
// persistent failure (a failing sandbox pool today, anything else tomorrow).
// The band carries `role="alert"` so assistive tech announces it, and it
// reserves its own layout space at the top of the dock region (it is NOT an
// out-of-flow overlay like the toasts) — a permanent problem deserves permanent
// space rather than floating over and hiding the feed.
import type { JSX } from 'react';
import type { SystemAlert } from '@/transport/transport';

export interface SystemAlertBandProps {
  alerts: SystemAlert[];
}

export function SystemAlertBand({ alerts }: SystemAlertBandProps): JSX.Element | null {
  if (alerts.length === 0) {
    // Healthy steady state: render nothing, so the dock region collapses back to
    // exactly the dock's height and the feed reclaims the space.
    return null;
  }

  return (
    <div data-role="system-alert-band" role="alert" aria-live="assertive">
      {alerts.map((alert, index) => (
        // Alerts carry no server id; index within this absolute snapshot is a
        // stable enough key (the whole array is replaced wholesale each board
        // event, so React never reconciles across snapshots meaningfully).
        <div
          key={`${alert.kind}-${index.toString()}`}
          data-role="system-alert"
          data-kind={alert.kind}
        >
          <span data-role="system-alert-icon" role="img" aria-label="Error">
            ⚠️
          </span>
          <span data-role="system-alert-detail">{alert.detail}</span>
        </div>
      ))}
    </div>
  );
}
