// The in-progress panel (13 §8.2, §10 "Working") — the desktop shell's answer to
// "what is being worked on right now".
//
// It replaces the bare one-word indication that used to sit at the head of the
// feed. That said *that* something was in motion; it never said *what*, so the
// only way to find out was to open tickets one at a time. The panel keeps the
// same register — one breathing dot, low contrast, no progress bar — and adds
// the subject: the tickets an agent is bound to, named, newest at the bottom.
//
// It renders in its OWN COLUMN, to the left of the feed and separated from it by
// a rule, rather than as a strip above the cards. That is the whole of "not
// buried in the feed": a column cannot be scrolled away by reading back through
// the history, and it does not compete with the feed's reading measure for the
// eye's starting point.
//
// The column is always present, even at rest. A panel that appeared and vanished
// with the work would shove the feed sideways every time an agent picked
// something up — the loudest possible way to announce a change, on a screen
// whose first principle is that change arrives without announcing itself
// (13 §1). So the geometry holds still and the CONTENT says whether anything is
// running: a breathing dot and rows when there is, one faint line when there
// isn't.
//
// What it deliberately is NOT: a count badge, a progress meter, a live log tail,
// or anything that ticks (13 §8). Each row is a status mark, a title, an
// optional word when the session behind it is not plainly building, and how long
// it has been in Working.
import type { JSX } from 'react';
import { relativeAge } from '@/components/feed-format';
import { workingStatusNote, type WorkingTicket } from '@/components/desktop/working-now';

export interface WorkingNowProps {
  /** The Working tickets, oldest-started first (see `workingTickets`). */
  tickets: WorkingTicket[];
  /** Whether anything is in motion at all — the brain mid-pass or workers
   * mid-turn. Kept separate from the list because the two can disagree in both
   * directions: the brain thinks with nothing in Working, and a board snapshot
   * can arrive before the feed summary catches up. Either one lights the panel. */
  active: boolean;
  /** Opens a ticket's detail sheet over the feed (13 D7). */
  onOpenTicket: (ticketId: string) => void;
  /** Injected "now" for deterministic relative-age rendering. */
  now: number;
}

/** How long a ticket has been worked, as a phrase rather than a bare token —
 * "12m" alone is fine beside a title but cryptic read aloud, and this is the
 * text the row's accessible name is built from. */
function agePhrase(since: string, now: number): string {
  const age = relativeAge(since, now);
  return age === 'now' ? 'just started' : `for ${age}`;
}

export function WorkingNow({ tickets, active, onOpenTicket, now }: WorkingNowProps): JSX.Element {
  // Live = something is actually running. It drives the breathing dot and, with
  // it, `role="status"`: an announcement belongs to the transition into work,
  // not to the resting panel, which would otherwise re-announce "working now"
  // on every unrelated re-render.
  const live = active || tickets.length > 0;

  return (
    <section data-role="desktop-working" aria-label="Working now">
      {/* `role="status"` is on the head line ONLY, not the whole panel: the
          section holds buttons, and wrapping interactive controls in a live
          region makes assistive tech re-announce them on every re-render. */}
      <div
        data-role="desktop-working-head"
        data-active={live ? 'true' : 'false'}
        role={live ? 'status' : undefined}
      >
        <span
          data-role="desktop-working-dot"
          data-active={live ? 'true' : 'false'}
          aria-hidden="true"
        />
        <span>working now</span>
      </div>
      {tickets.length > 0 ? (
        <ul data-role="desktop-working-list">
          {tickets.map((ticket) => {
            const note = workingStatusNote(ticket.status);
            return (
              <li key={ticket.id}>
                <button
                  type="button"
                  data-role="desktop-working-ticket"
                  data-status={ticket.status}
                  // Spelled out rather than left to the row's text content, so
                  // the bare "12m" becomes a sentence and the status word is
                  // stated even when the visible note is empty.
                  aria-label={`Open working ticket: ${ticket.title} — ${
                    note === '' ? 'working' : note
                  } ${agePhrase(ticket.since, now)}`}
                  onClick={() => {
                    onOpenTicket(ticket.id);
                  }}
                >
                  {/* The SAME mark the phone's ticket list uses, from the same
                      unscoped rules in PrimaryScreen.css — accent and pulsing
                      while a session builds, amber while it starts, hollow when
                      it has stopped, red when it has failed. Reusing the element
                      rather than restating the palette here is what keeps
                      "in progress" looking identical on both platforms; a second
                      set of colours would drift the moment either is tuned. */}
                  <span data-role="status-dot" data-status={ticket.status} aria-hidden="true" />
                  <span data-role="desktop-working-title">{ticket.title}</span>
                  <span data-role="desktop-working-meta">
                    {note !== '' && <span data-role="desktop-working-note">{note}</span>}
                    <span data-role="desktop-working-age">{relativeAge(ticket.since, now)}</span>
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      ) : (
        // The resting state is the real state (13 §1). One flat line, in the
        // faintest ink on the screen — it reports an absence, so it must not
        // read as a region waiting to be dealt with.
        <p data-role="desktop-working-empty">Nothing in progress.</p>
      )}
    </section>
  );
}
