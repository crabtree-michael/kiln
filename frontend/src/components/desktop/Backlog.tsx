// The backlog section of the desktop shell's left panel — what is queued up but
// not started yet, directly under the in-progress list it shares a column with.
//
// It is the same answer the phone's tickets dropdown already gives (the ready
// backlog listed under the active tickets), brought to the desk. Without it the
// column reported only the tickets an agent happened to be holding, so a project
// with four workers busy and eleven tickets queued behind them looked, at a
// desk, exactly like a project with four tickets in it.
//
// It shares the working list's register down to the row: the same shared status
// mark, the same two-line row, the same faint time-in-status. What it never
// shares is the LIVENESS — nothing here is running, so nothing here breathes and
// there is no live region. That absence is the difference between the two
// sections, and it is the whole reason they can sit in one column without
// reading as one list.
//
// What it deliberately is NOT (13 §8, same list the working strip is held to): a
// count, a queue-depth meter, a "3 waiting" badge. The panel lists; the kanban
// board at /kanban is where depth is a number.
//
// With an empty backlog the section is not drawn at all — head included. It
// exists to name what is queued, and with nothing queued there is nothing to
// name: a permanent heading over a permanent "Nothing waiting." is a region the
// eye keeps returning to for an answer it already has. The working section above
// it still states its own absence, because "nothing is running" is news about a
// project; "nothing is waiting" is just the backlog's resting size.
import type { JSX } from 'react';
import { relativeAge } from '@/components/feed-format';
import { backlogStateNote, type BacklogTicket } from '@/components/desktop/backlog';

export interface BacklogProps {
  /** The waiting tickets, ready queue first, in the server's order (see
   * `backlogTickets` — the order carries the pull sequence). */
  tickets: BacklogTicket[];
  /** Opens a ticket's detail sheet over the feed (13 D7) — the same sheet, and
   * the same overlay, a working row opens. A backlog ticket is still editable
   * there, which is the second reason these rows are worth having: shaping and
   * ready are exactly the states the sheet lets the user rewrite. */
  onOpenTicket: (ticketId: string) => void;
  /** Injected "now" for deterministic relative-age rendering. */
  now: number;
}

/** How long a ticket has been waiting, as a phrase rather than a bare token —
 * "12m" alone is fine beside a title but cryptic read aloud, and this is the
 * text the row's accessible name is built from. Mirrors the working row's
 * `agePhrase`, in this section's own tense. */
function waitPhrase(since: string, now: number): string {
  const age = relativeAge(since, now);
  return age === 'now' ? 'just added' : `waiting for ${age}`;
}

export function Backlog({ tickets, onOpenTicket, now }: BacklogProps): JSX.Element | null {
  // Nothing queued, nothing drawn. Also covers the pre-snapshot case, where
  // `backlogTickets` returns [] for a null board: the section staying away until
  // the board lands is right for the same reason it stays away when the queue is
  // genuinely empty — better silent than asserting an empty backlog we have not
  // been told about yet.
  if (tickets.length === 0) {
    return null;
  }
  return (
    <section data-role="desktop-backlog" aria-label="Backlog">
      {/* No dot and no `role="status"`, unlike the working head above it.
          Nothing in this section is in motion, so a mark that could breathe
          would have nothing to report, and a live region would re-announce a
          queue that has not changed on every unrelated re-render. The head is a
          label, and it stays one. */}
      <div data-role="desktop-backlog-head">backlog</div>
      <ul data-role="desktop-backlog-list">
        {tickets.map((ticket) => {
          const note = backlogStateNote(ticket);
          return (
            <li key={ticket.id}>
              <button
                type="button"
                data-role="desktop-backlog-ticket"
                data-state={ticket.state}
                // Spelled out rather than left to the row's text content, so
                // the bare "12m" becomes a sentence and the ticket's reading
                // is stated even when the visible note is empty.
                aria-label={`Open backlog ticket: ${ticket.title} — ${
                  note === '' ? 'ready' : note
                }, ${waitPhrase(ticket.since, now)}`}
                onClick={() => {
                  onOpenTicket(ticket.id);
                }}
              >
                {/* The SAME mark the phone's ticket list and the working rows
                    above use, from the same unscoped rules in
                    PrimaryScreen.css. `data-state` is the ticket's own
                    lifecycle state and picks the ink — shaping and ready are
                    the two states the detail sheet gives no badge either, so
                    both land on the mark's flat, faint default, which is
                    exactly right for a ticket nothing is happening to.

                    No `data-status`: that attribute is the bound session's,
                    and a backlog ticket has no worker. Inventing one here
                    (`idle`, say) would texture the mark with a session that
                    does not exist. */}
                <span data-role="status-dot" data-state={ticket.state} aria-hidden="true" />
                <span data-role="desktop-backlog-title">{ticket.title}</span>
                {/* The subtitle line, in the same order the phone's ticket
                    dropdown states it (`rowSubtitle`): the note — a dependency
                    label, or the state word — then the dot, then the time in
                    status. The dot is the separator here rather than the flex
                    gap alone, so the note reads as bound to the age instead of
                    merely sitting near it, and so the desk says this the way the
                    phone does. Two spans rather than one string, unlike the
                    phone: the age is faint-and-firming under the pointer here
                    (13 §6) and the note is not, which one text node cannot do. */}
                <span data-role="desktop-backlog-meta">
                  {note !== '' && (
                    <>
                      <span data-role="desktop-backlog-note">{note}</span>
                      <span data-role="desktop-backlog-sep" aria-hidden="true">
                        ·
                      </span>
                    </>
                  )}
                  <span data-role="desktop-backlog-age">{relativeAge(ticket.since, now)}</span>
                </span>
              </button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
