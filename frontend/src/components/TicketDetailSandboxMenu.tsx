// The ticket detail sheet's sandbox menu: one gear beside the lifecycle badge,
// opening a dropdown that holds every sandbox decision for this ticket. It
// replaces the row of buttons that used to sit at the foot of the sheet's body —
// a save switch, a Kill button and a Move button, each with its own paragraph of
// explanation, all competing with the ticket's own text for the reader's
// attention. These are rare, considered actions on the *workspace* behind the
// ticket rather than anything about the work itself, so they belong folded away
// behind an icon: present on every ticket, in the way of nothing.
//
// Three items, each self-gating on its callback being wired (an omitted one is
// simply absent, so a read-only sheet shows no menu at all):
//
//   • Save sandbox when done — a toggle carrying a checkmark when on. The one
//     non-destructive item, and the only one offered on a ticket with no sandbox
//     yet: the choice matters before the workspace exists just as much as while
//     it runs.
//   • Re-create sandbox — throw this workspace away and bring a fresh one up on
//     the same slot, leaving the ticket where it is.
//   • Move to free sandbox — rebind the ticket to a free slot and brief an agent
//     there from scratch. Shown only when a slot is actually free (the caller
//     passes the callback only then), because with every sandbox busy the server
//     would refuse it and offering a dead item is worse than offering none.
//
// Both destructive items destroy in-progress work irreversibly, so each is gated
// behind a native confirm that names what is lost — the same gate the sheet's
// blocked-ticket Delete uses. That replaced a two-tap arming dance: inside a
// menu that closes on the first tap there is nowhere for an armed state to live,
// and a confirm dialog says what the second tap commits to far more plainly than
// a re-labelled button could.
//
// Dropdown mechanics (click-outside / Escape to dismiss, panel stays mounted so
// it animates both ways) mirror NotificationSettingsMenu, the app's other
// icon-triggered menu.
import { useEffect, useRef, useState, type JSX } from 'react';

export interface TicketDetailSandboxMenuProps {
  /** The ticket every action names. Also the reset key: changing it closes the
   * menu, so a sheet that swaps to another ticket can't leave a dropdown open
   * over a different sandbox than the one it was opened for. */
  ticketId: string;
  /** What the toggle renders as — the caller's optimistic value, not the raw
   * board field, so the checkmark lands on the tap rather than a snapshot later. */
  keepSandbox: boolean;
  /** Persist the sandbox-saving choice. Omitted → no toggle item. */
  onSetKeepSandbox?: ((ticketId: string, keep: boolean) => void) | undefined;
  /** Destroy this ticket's workspace and bring a fresh one up on the same slot.
   * Omitted → no Re-create item; the caller passes it only on a ticket that has
   * a sandbox to act on. */
  onKillSandbox?: ((ticketId: string) => void) | undefined;
  /** Rebind this ticket to a free sandbox and start the work over there. Omitted
   * → no Move item; the caller passes it only when the ticket has a sandbox AND
   * the board reports a free slot to move it to. */
  onReassignSandbox?: ((ticketId: string) => void) | undefined;
  /** The sandbox's live session status, already in user-facing words. It heads
   * the menu so the destructive items are read against what the sandbox is
   * actually doing — cutting a turn short is a different decision from clearing
   * a failed one. Omitted (a ticket with no sandbox) → no status line. */
  sandboxStatusLabel?: string | undefined;
}

/** Confirm copy for Re-create. It names the two things the user cannot see from
 * here: the workspace goes (with anything uncommitted in it) and whatever the
 * agent is doing right now stops. */
const RECREATE_CONFIRM =
  'Re-create this sandbox? Its workspace is thrown away and a fresh one comes up in its place — ' +
  'any ongoing work is killed and cannot be recovered here.';

/** Confirm copy for Move. Distinct from Re-create's because the loss is
 * different in kind: the ticket lives on in a *clean* sandbox, briefed from its
 * work order again, so what goes is everything the current agent has done. */
const MOVE_CONFIRM =
  'Move this ticket to a free sandbox? The work starts over there from the ticket, so any ' +
  'ongoing work in the current sandbox is lost.';

export function TicketDetailSandboxMenu({
  ticketId,
  keepSandbox,
  onSetKeepSandbox,
  onKillSandbox,
  onReassignSandbox,
  sandboxStatusLabel,
}: TicketDetailSandboxMenuProps): JSX.Element {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  // A sheet kept mounted across a ticket change (the board snapshot swapping
  // which ticket is open) must not carry an open menu with it: the items would
  // then be aimed at a sandbox the user never opened the menu for.
  useEffect(() => {
    setOpen(false);
  }, [ticketId]);

  // While open, a click anywhere outside — or Escape — dismisses it (mirrors
  // NotificationSettingsMenu). Escape has to reach this menu BEFORE the sheet:
  // the sheet is a Radix dialog and Radix listens for the key in the capture
  // phase on `document`, so a plain listener (either phase) would see the sheet
  // already dismissed underneath. Hence capture on **window** — the first node in
  // the propagation path, ahead of document — plus `stopPropagation`, which is
  // what makes the key close the topmost layer only. It is restored the moment
  // the menu closes, so Escape means "close the sheet" again.
  //
  // (`capture: true` on document would NOT be enough: Radix registers on the
  // same node in the same phase and mounts first, so it would still run first.)
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
        event.stopPropagation();
        setOpen(false);
      }
    }
    document.addEventListener('mousedown', onPointerDown);
    window.addEventListener('keydown', onKeyDown, true);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      window.removeEventListener('keydown', onKeyDown, true);
    };
  }, [open]);

  const panelId = `ticket-sandbox-menu-${ticketId}`;

  return (
    // `data-vaul-no-drag`: the sheet reads pointer drags as a dismiss gesture, so
    // opt the whole menu out — a press that lands on an item must not take the
    // sheet away under it.
    <div data-role="detail-sandbox-menu" ref={rootRef} data-vaul-no-drag>
      <button
        type="button"
        data-role="detail-sandbox-trigger"
        data-open={open}
        aria-haspopup="true"
        aria-expanded={open}
        aria-controls={panelId}
        aria-label="Sandbox options"
        onClick={() => {
          setOpen((wasOpen) => !wasOpen);
        }}
      >
        {/* The gear is the whole visible signal; the accessible name is the
            aria-label, matching the header's pencil and the dock's trash. */}
        <svg
          viewBox="0 0 24 24"
          width="15"
          height="15"
          aria-hidden="true"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.6"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <circle cx="12" cy="12" r="3" />
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.6 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.6a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9v.09a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
        </svg>
      </button>
      {/* The panel stays mounted while closed so it animates in both directions;
          `aria-hidden` is what actually takes it out of the page (and out of a
          query for its items) until the gear is tapped. */}
      <div id={panelId} data-role="detail-sandbox-panel" data-open={open} aria-hidden={!open}>
        {sandboxStatusLabel !== undefined && (
          <div data-role="detail-sandbox-menu-status">
            This ticket&rsquo;s sandbox is {sandboxStatusLabel}.
          </div>
        )}
        {/* `role="menu"` rides on the list itself, with the <li>s presentational:
            a menu's children have to be its items, and a listitem between them
            is not one. */}
        <ul data-role="detail-sandbox-menu-list" role="menu">
          {onSetKeepSandbox !== undefined && (
            <li role="none">
              <button
                type="button"
                role="menuitemcheckbox"
                data-role="detail-keep-sandbox"
                data-checked={keepSandbox}
                aria-checked={keepSandbox}
                // The menu stays open on a toggle, unlike the two actions below:
                // the checkmark appearing under the pointer IS the feedback, and
                // closing the panel would hide the only thing that changed.
                onClick={() => {
                  onSetKeepSandbox(ticketId, !keepSandbox);
                }}
              >
                <span data-role="detail-sandbox-menu-check" aria-hidden="true">
                  <svg
                    viewBox="0 0 24 24"
                    width="13"
                    height="13"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2.4"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <path d="M5 12.5l4.5 4.5L19 7" />
                  </svg>
                </span>
                <span data-role="detail-sandbox-menu-label">Save sandbox when done</span>
              </button>
            </li>
          )}
          {onKillSandbox !== undefined && (
            <li role="none">
              <button
                type="button"
                role="menuitem"
                data-role="detail-kill-sandbox"
                data-tone="danger"
                onClick={() => {
                  if (!window.confirm(RECREATE_CONFIRM)) {
                    return;
                  }
                  setOpen(false);
                  onKillSandbox(ticketId);
                }}
              >
                <span data-role="detail-sandbox-menu-check" aria-hidden="true" />
                <span data-role="detail-sandbox-menu-label">Re-create sandbox</span>
              </button>
            </li>
          )}
          {onReassignSandbox !== undefined && (
            <li role="none">
              <button
                type="button"
                role="menuitem"
                data-role="detail-reassign-sandbox"
                data-tone="danger"
                onClick={() => {
                  if (!window.confirm(MOVE_CONFIRM)) {
                    return;
                  }
                  setOpen(false);
                  onReassignSandbox(ticketId);
                }}
              >
                <span data-role="detail-sandbox-menu-check" aria-hidden="true" />
                <span data-role="detail-sandbox-menu-label">Move to free sandbox</span>
              </button>
            </li>
          )}
        </ul>
      </div>
    </div>
  );
}
