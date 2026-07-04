// Developer "Reset session" button (debug view only). One click, guarded by a
// confirm dialog, returns the whole system to a fresh agent session: the
// backend wipes the board + chat and tears down the live sandboxes, then the
// page reloads so the client re-fetches the now-empty board and reopens the
// stream. Lives only in the /debug App shell — it never reaches the primary
// client. See docs/superpowers/specs/2026-07-04-debug-reset-session-design.md.
import { useState, type JSX } from 'react';
import { postReset } from '@/transport/transport';

const CONFIRM_MESSAGE = 'Reset to a fresh session? This wipes the board, chat, and all sandboxes.';

export function ResetSessionButton(): JSX.Element {
  const [busy, setBusy] = useState(false);

  async function handleClick(): Promise<void> {
    if (busy) return;
    if (!window.confirm(CONFIRM_MESSAGE)) return;
    setBusy(true);
    try {
      await postReset();
      window.location.reload();
    } catch {
      // Leave the page as-is on failure so nothing looks reset that isn't; the
      // developer can retry. Return the button to idle.
      setBusy(false);
    }
  }

  return (
    <button
      type="button"
      className="reset-session-button"
      onClick={() => void handleClick()}
      disabled={busy}
    >
      {busy ? 'Resetting…' : 'Reset session'}
    </button>
  );
}
