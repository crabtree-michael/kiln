// The top-right header status, now a clickable dropdown (08 §2). Collapsed it
// shows the same one-line summary as before (`feedStatus`); expanded it lists
// every ticket on the board (amended 2026-07-06) — active ones first, then the
// rest in decreasing-activity order with backlog at the bottom. Presentational:
// the board comes in as a prop (PrimaryScreen bridges the board store),
// open/close is local UI state. The panel stays mounted so it animates both ways.
import { useEffect, useRef, useState, type JSX } from 'react';
import type { Board, FeedSummary } from '@/transport/transport';
import { feedStatus, relativeAge, ticketStatuses } from '@/components/feed-format';

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
}

export function HeaderStatusMenu({
  summary,
  board,
  onOpen,
  refreshing = false,
}: HeaderStatusMenuProps): JSX.Element {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const tickets = ticketStatuses(board);

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
        {feedStatus(summary)}
        <span data-role="feed-status-caret" aria-hidden="true" />
      </button>
      <div
        id="header-status-panel"
        data-role="header-status-panel"
        data-open={open}
        aria-hidden={!open}
      >
        <div data-role="header-status-heading">Tickets</div>
        {refreshing && tickets.length === 0 ? (
          <div data-role="header-status-loading">
            <span data-role="header-status-spinner" aria-hidden="true" />
            <span>Loading tickets…</span>
          </div>
        ) : tickets.length === 0 ? (
          <div data-role="header-status-empty">No tickets</div>
        ) : (
          <ul data-role="header-status-list">
            {tickets.map((ticket) => (
              <li key={ticket.id} data-role="header-status-row" data-status={ticket.status}>
                <span data-role="header-status-dot" aria-hidden="true" />
                <span data-role="header-status-label">{ticket.label || 'Untitled ticket'}</span>
                <span data-role="header-status-age">{relativeAge(ticket.updatedAt)}</span>
                {ticket.reason !== null && (
                  <span data-role="header-status-reason">{ticket.reason}</span>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
