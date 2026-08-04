// The working strip (13 §8.2, §10 "Working") — the desktop shell's answer to
// "what is being worked on right now".
//
// It replaces the bare one-word indication that used to sit at the head of the
// feed. That said *that* something was in motion; it never said *what*, so the
// only way to find out was to open tickets one at a time. The strip keeps the
// same register — one breathing dot, low contrast, no progress bar — and adds
// the subject: the tickets an agent is bound to, named, newest at the bottom.
//
// It is rendered IN FLOW above the feed's scroll region rather than as the
// column's first row, the same call `SystemAlertBand` makes on mobile: a fact
// that stays true for as long as the work runs should reserve its own height
// and stay put, not scroll out of sight the moment you read back through the
// history. That is the whole of "not buried in the feed".
//
// What it deliberately is NOT: a count badge, a progress meter, a live log tail,
// or anything that ticks (13 §8). Each row is a title, an optional word when the
// session behind it is not plainly building, and how long it has been in
// Working. The strip carries no accent — that budget is spent once, on the
// rail's needs-you dot (13 §4).
import type { JSX } from 'react';
import { relativeAge } from '@/components/feed-format';
import { workingStatusNote, type WorkingTicket } from '@/components/desktop/working-now';

export interface WorkingNowProps {
  /** The Working tickets, oldest-started first (see `workingTickets`). */
  tickets: WorkingTicket[];
  /** Whether anything is in motion at all — the brain mid-pass or workers
   * mid-turn. Kept separate from the list because the two can disagree in both
   * directions: the brain thinks with nothing in Working, and a board snapshot
   * can arrive before the feed summary catches up. Either one lights the strip. */
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

export function WorkingNow({
  tickets,
  active,
  onOpenTicket,
  now,
}: WorkingNowProps): JSX.Element | null {
  // Nothing running and nothing thinking: the strip does not exist at all — the
  // same shape `SystemAlertBand` uses for a healthy board, and for the same
  // reason. The resting state is the real state (13 §1); an "idle" reading here
  // would be a permanently-lit region reporting the absence of news.
  if (!active && tickets.length === 0) {
    return null;
  }

  return (
    <section data-role="desktop-working" aria-label="Working now">
      {/* `role="status"` is on the head line ONLY, not the whole strip: the
          section holds buttons, and wrapping interactive controls in a live
          region makes assistive tech re-announce them on every re-render. */}
      <div data-role="desktop-working-head" role="status">
        <span data-role="desktop-working-dot" aria-hidden="true" />
        <span>working now</span>
      </div>
      {tickets.length > 0 && (
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
                  <span data-role="desktop-working-title">{ticket.title}</span>
                  {note !== '' && <span data-role="desktop-working-note">{note}</span>}
                  <span data-role="desktop-working-age">{relativeAge(ticket.since, now)}</span>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
