// The top-right header status, now a clickable dropdown (08 §2). Collapsed it
// shows a one-line summary counting the same tickets the panel lists — active
// and queued (`feedStatus`); expanded it lists every ticket on the board
// (amended 2026-07-06) — active ones first, then the rest in decreasing-activity
// order with backlog at the bottom. Presentational:
// the board comes in as a prop (PrimaryScreen bridges the board store),
// open/close is local UI state. The panel stays mounted so it animates both ways.
import { useEffect, useRef, useState, type HTMLAttributes, type JSX } from 'react';
import type { Board, FeedSummary } from '@/transport/transport';
import {
  feedStatus,
  relativeAge,
  rowSubtitle,
  ticketStatuses,
  waitingOnLabel,
} from '@/components/feed-format';

export interface HeaderStatusMenuProps {
  summary: FeedSummary;
  board: Board | null;
  /** Fired when the panel opens (closed → open). Triggers an independent board
   * fetch so the ticket list reflects current state rather than waiting for
   * the next agent-driven push. Optional so presentational tests can omit it. */
  onOpen?: (() => void) | undefined;
  /** True while that fetch is in flight. When there's nothing to show yet the
   * panel renders a loading indicator instead of "No tickets", so a genuinely
   * empty board reads differently from one still loading. */
  refreshing?: boolean;
  /** Open a ticket's detail sheet by id. When supplied, each row becomes
   * clickable (and keyboard-actionable) and selecting one dismisses the menu;
   * the parent resolves the id against the live board to drive the existing
   * TicketDetail overlay. Optional so presentational tests can omit it — the
   * rows then stay purely presentational, as before. */
  onSelectTicket?: ((ticketId: string) => void) | undefined;
}

export function HeaderStatusMenu({
  summary,
  board,
  onOpen,
  refreshing = false,
  onSelectTicket,
}: HeaderStatusMenuProps): JSX.Element {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const tickets = ticketStatuses(board);
  // What the head reports: the ticket at the top of the list under it. The desk's
  // in-progress panel has worn its rows' own mark for a while (13 §8.2); the phone's
  // list had the marks and no reading over them, so the one line naming the list
  // said nothing about the state of it.
  //
  // The TOP ticket rather than a state derived here, because `ticketStatuses`
  // already ranks the list working → blocked → ready: the first row IS the loudest
  // thing on the board, and re-deriving that as a second rule is how the head ends
  // up disagreeing with the row directly beneath it. Undefined before the first
  // snapshot and on an empty board — the mark then falls back to its stateless
  // default (flat, faint, no halo), which is the right reading for a head with
  // nothing to report.
  const top = tickets[0];
  // The collapsed badge counts exactly what the dropdown lists — active *and*
  // queued tickets — so the number matches once opened. Before the first board
  // snapshot (`board` null) we fall back to the summary's active count so the
  // badge isn't wrongly "Nothing active" while streams are already live.
  const ticketCount = board === null ? summary.stream_count : tickets.length;

  // While open, a click anywhere outside the menu — or Escape — dismisses it.
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

  return (
    <div data-role="header-status" ref={rootRef}>
      <button
        type="button"
        data-role="feed-status"
        data-open={open}
        aria-haspopup="true"
        aria-expanded={open}
        aria-controls="header-status-panel"
        onClick={() => {
          // Opening pulls a fresh snapshot; `open` is the current render's state,
          // so this reads the pre-toggle value and only fires on closed → open.
          if (!open) {
            onOpen?.();
          }
          setOpen((wasOpen) => !wasOpen);
        }}
      >
        {feedStatus(ticketCount)}
        <span data-role="feed-status-caret" aria-hidden="true" />
      </button>
      <div
        id="header-status-panel"
        data-role="header-status-panel"
        data-open={open}
        aria-hidden={!open}
      >
        <div data-role="header-status-heading">
          {/* The SAME element the rows below render — the shared `status-dot`
              from PrimaryScreen.css, keyed on the same two attributes, not a
              second mark of the heading's own. Size, ink, texture and cadence
              all come from the one rule that paints the list, so the head
              cannot look like a different kind of thing from what it
              summarises, and neither can drift when the other is tuned.

              Phase comes with it. `[data-status='building']` carries
              `--pulse-phase: shared`, so this mark is pinned to the same
              document-timeline clock as every other breathing mark on screen
              (pulse-phase.ts) — it breathes WITH the row it is reporting rather
              than merely at the same rate, which is the difference between one
              reading and two marks shimmering against each other.

              No `role="status"` on the head, unlike the desk's: that panel is
              permanent and announces the transition into work, while this one
              is a dropdown the user has just opened — a live region here would
              re-announce the heading on every unrelated re-render behind it. */}
          <span
            data-role="status-dot"
            data-state={top?.state}
            data-status={top?.status}
            aria-hidden="true"
          />
          <span>Tickets</span>
        </div>
        {refreshing && tickets.length === 0 ? (
          <div data-role="header-status-loading">
            <span data-role="header-status-spinner" aria-hidden="true" />
            <span>Loading tickets…</span>
          </div>
        ) : tickets.length === 0 ? (
          <div data-role="header-status-empty">No tickets</div>
        ) : (
          <ul data-role="header-status-list">
            {tickets.map((ticket) => {
              // Narrow on `onSelectTicket` directly (not a derived boolean) so
              // TypeScript knows it is defined inside the handlers — the lint
              // gate rejects the optional chain as unnecessary (mirrors
              // TicketCard's onSelect).
              const select =
                onSelectTicket === undefined
                  ? undefined
                  : () => {
                      onSelectTicket(ticket.id);
                      setOpen(false);
                    };
              const interactiveProps: HTMLAttributes<HTMLLIElement> =
                select === undefined
                  ? {}
                  : {
                      role: 'button',
                      tabIndex: 0,
                      'aria-label': `Open ticket: ${ticket.label || 'Untitled ticket'}`,
                      onClick: select,
                      onKeyDown: (event) => {
                        if (event.key === 'Enter' || event.key === ' ') {
                          event.preventDefault();
                          select();
                        }
                      },
                    };
              return (
                <li
                  key={ticket.id}
                  data-role="header-status-row"
                  data-status={ticket.status}
                  data-interactive={select !== undefined ? 'true' : undefined}
                  {...interactiveProps}
                >
                  {/* The shared status mark (PrimaryScreen.css). Both attributes
                      ride on the dot rather than being inherited from the row,
                      so the desktop in-progress panel — whose rows are buttons
                      with a different layout — renders the identical vocabulary
                      from the identical rules. `data-state` is the ticket's own
                      lifecycle state and picks the colour (the sheet's colour);
                      `data-status` is the session behind it and picks the
                      texture. */}
                  <span
                    data-role="status-dot"
                    data-state={ticket.state}
                    data-status={ticket.status}
                    aria-hidden="true"
                  />
                  <span data-role="header-status-label">{ticket.label || 'Untitled ticket'}</span>
                  {/* The subtitle line: the dependency label, when unlanded work
                      is holding a queued ticket, then the time in status. It
                      rides IN the age span rather than beside it — the label
                      arrived as a sibling with no rule of its own, which the
                      row's grid auto-placed onto a third line at the row's own
                      size and ink, reading as a separate fact rather than as the
                      qualifier on the time it belongs to. Sharing the span makes
                      the existing 11px/faint subtext rule the only styling
                      either half needs.

                      A row can never carry both this and a blocked reason: the
                      server only derives `waiting_on_dependencies` for a ready
                      ticket, and `blocked_reason` is set iff blocked. */}
                  <span data-role="header-status-age">
                    {rowSubtitle(
                      ticket.waitingOn > 0 ? waitingOnLabel(ticket.waitingOn) : '',
                      relativeAge(ticket.statusSince),
                    )}
                  </span>
                  {ticket.reason !== null && (
                    <span data-role="header-status-reason">{ticket.reason}</span>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}
