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
import {
  workingPanelLabel,
  workingPanelState,
  workingStatusNote,
  type WorkingTicket,
} from '@/components/desktop/working-now';

export interface WorkingNowProps {
  /** The Working tickets, oldest-started first (see `workingTickets`). */
  tickets: WorkingTicket[];
  /** How many tickets are blocked. Only the count: blocked tickets are not
   * listed here — the head says the project is stuck, and the feed and the
   * ticket sheet say which one and why. */
  blocked: number;
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

export function WorkingNow({
  tickets,
  blocked,
  active,
  onOpenTicket,
  now,
}: WorkingNowProps): JSX.Element {
  // Live = something is actually running. It drives the breathing dot and, with
  // it, `role="status"`: an announcement belongs to the transition into work,
  // not to the resting panel, which would otherwise re-announce the head on
  // every unrelated re-render.
  //
  // Kept separate from the head's WORD (below), which reads the board. The two
  // answer different questions — "is anything happening" vs "what state is this
  // project in" — and a brain pass over an empty board is a true answer to the
  // first and not to the second: the dot breathes, the word still says idle,
  // because there is no ticket for it to be naming.
  const live = active || tickets.length > 0;

  // What the head says, and what it wears. Both come from the board rather than
  // from a fixed string, so the panel reports a state instead of labelling a
  // column — see `workingPanelState` for why working outranks blocked.
  const state = workingPanelState(tickets.length, blocked);

  // The head is a summary of the list under it, so it wears the colour those
  // tickets wear rather than a fixed grey of its own — a head that stayed
  // neutral above a list of live work read as a second, contradicting status.
  //
  // The colour comes from the TICKET's lifecycle state, which is why these are
  // `working` and `blocked` (the same values `ticket.state` carries into the
  // detail sheet's badge) and not one of the session statuses the rows below key
  // on. Every ticket listed in this panel is in Working, so opening any of them
  // shows an IN PROGRESS badge; the ticket's own reading is the source of truth
  // and the head matches it. Keying off a row's session status instead would
  // make the head disagree with the detail view of the very ticket it sits
  // above.
  //
  // Undefined at rest — an idle project has no ticket to take a colour from, and
  // falls back to the neutral reading in CSS. That is the one state that should
  // be quiet; naming it in an ink of its own would give the eye something to
  // land on where there is nothing to do.
  const headState = state === 'idle' ? undefined : state;

  return (
    // The region's name is fixed while the head's word is not: a landmark that
    // renamed itself as the project moved would shuffle under anyone navigating
    // by region. The state is spoken by the live head inside it instead.
    <section data-role="desktop-working" aria-label="In progress">
      {/* `role="status"` is on the head line ONLY, not the whole panel: the
          section holds buttons, and wrapping interactive controls in a live
          region makes assistive tech re-announce them on every re-render. */}
      <div
        data-role="desktop-working-head"
        data-active={live ? 'true' : 'false'}
        data-state={headState}
        role={live ? 'status' : undefined}
      >
        <span
          data-role="desktop-working-dot"
          data-active={live ? 'true' : 'false'}
          aria-hidden="true"
        />
        <span>{workingPanelLabel(state)}</span>
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
                      unscoped rules in PrimaryScreen.css — the ticket's ember
                      while it is worked (breathing while a session builds, flat
                      while it is idle, hollow once it has stopped), fire only
                      when the session has failed. Reusing the element rather
                      than restating the palette here is what keeps "in progress"
                      looking identical on both platforms; a second set of
                      colours would drift the moment either is tuned.

                      `data-state` is literal because every row in this panel is
                      a ticket in Working — the same value the head above takes,
                      and the same one the detail sheet's badge is keyed on, so
                      head, row, and sheet cannot disagree about a ticket the
                      user can see all three readings of. */}
                  <span
                    data-role="status-dot"
                    data-state="working"
                    data-status={ticket.status}
                    aria-hidden="true"
                  />
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
        //
        // Except when the absence has a cause: nothing running BECAUSE a ticket
        // is waiting on a decision is not the same fact as nothing to run, and
        // the head's one word cannot carry the difference on its own. "needs
        // you" is the rail's phrase for exactly this, kept verbatim so the two
        // surfaces say the same thing about the same board. Still no count —
        // this panel lists, it does not measure (13 §8), and the blocker itself
        // is a card in the feed, pinned, with its reason.
        <p data-role="desktop-working-empty">
          {state === 'blocked'
            ? 'Nothing in progress — a ticket needs you.'
            : 'Nothing in progress.'}
        </p>
      )}
    </section>
  );
}
