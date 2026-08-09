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
// It lists the BLOCKED tickets too, above the working ones (amended
// 2026-08-09). They used to reach this panel as a bare count — enough to colour
// the head and to add half a sentence to the resting line, never enough to name
// the ticket — on the reasoning that the blocker is a pinned card in the feed
// with its reason on it. That holds right up until the feed is scrolled, or the
// card has been seen and collapsed away, or a second project's worth of history
// sits on top of it; the column beside it, which cannot be scrolled away and
// exists precisely to list what is standing open, said only that *something*
// needed the user. The one state on the board that is waiting on a person was
// the one this panel would not name. See `activeTickets` for why the two states
// share one list and why blocked leads it.
//
// What it deliberately is NOT: a count badge, a progress meter, a live log tail,
// or anything that ticks (13 §8). Each row is a status mark, a title, a word
// where the ticket is not simply building, and how long it has been in its
// state. The blocker's REASON is still not here — that is a paragraph, and this
// column is a list of titles; the feed card and the detail sheet the row opens
// are where the question is read and answered.
import type { JSX } from 'react';
import { relativeAge } from '@/components/feed-format';
import {
  activeStatusNote,
  workingPanelLabel,
  workingPanelState,
  type ActiveTicket,
} from '@/components/desktop/working-now';

export interface WorkingNowProps {
  /** The started-and-unfinished tickets — blocked first, then working, each
   * group oldest-first (see `activeTickets`). */
  tickets: ActiveTicket[];
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
  // What the head says, and what it wears. Both come from the board rather than
  // from a fixed string, so the panel reports a state instead of labelling a
  // column — see `workingPanelState` for why working outranks blocked.
  const state = workingPanelState(tickets);

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
  //
  // It is the WORKING rows, not the row count, that count as motion — a listed
  // blocked ticket is the opposite of something happening, and reading a
  // non-empty list as liveness would have set a stuck panel breathing the moment
  // its blockers became visible.
  const live = active || state === 'working';

  // The head is a summary of the list under it, so it wears the colour those
  // tickets wear rather than a fixed grey of its own — a head that stayed
  // neutral above a list of live work read as a second, contradicting status.
  //
  // The colour comes from the TICKET's lifecycle state, which is why these are
  // `working` and `blocked` (the same values `ticket.state` carries into the
  // detail sheet's badge) and not one of the session statuses the rows below key
  // on. It is the state of the row it is NAMING: `working` while there is work
  // to name, `blocked` when the only thing left to name is stuck. So a head in
  // ember over a mixed list is the head of the working rows, and the fire row
  // above them wears its own ink and says its own word — one fact per level,
  // which is the same division the rows' session statuses already live under.
  // Keying off a row's session status instead would make the head disagree with
  // the detail view of the very ticket it sits above.
  //
  // Undefined at rest — an idle project has no ticket to take a colour from, and
  // falls back to the neutral reading in CSS. That is the one state that should
  // be quiet; naming it in an ink of its own would give the eye something to
  // land on where there is nothing to do.
  const headState = state === 'idle' ? undefined : state;

  // The head's mark is the SAME element as the rows' (see the mark below), so
  // its liveness is spelled in the same vocabulary: `building` is the one status
  // the shared mark breathes on, and the head breathes exactly when something is
  // running. Blocked is the deliberate hole in that — the state where nothing is
  // moving holds still even while the brain takes a pass behind it, because a
  // pulsing mark in the alarm ink is more than "one ticket needs a decision" is
  // worth, and the badge this head borrows from is "stuck, not moving".
  //
  // It is the session vocabulary on a mark that summarises tickets, which is a
  // small stretch: with a lone idle worker in Working the head breathes where its
  // one row sits flat. That is the reading the panel has always given (its head
  // breathes on `live`), and the alternative — a head that went still while work
  // was assigned — would drop the liveness signal the column exists to carry.
  const headStatus = live && state !== 'blocked' ? 'building' : undefined;

  return (
    // The region's name is fixed while the head's word is not: a landmark that
    // renamed itself as the project moved would shuffle under anyone navigating
    // by region. The state is spoken by the live head inside it instead.
    //
    // "Active", not "In progress", since the blocked rows arrived: IN PROGRESS
    // is the detail sheet's badge for `working` specifically, so a landmark
    // called that over a list of blocked tickets would be using the app's own
    // word for the one state those rows are not in. Active is the phone's
    // vocabulary for exactly this pair (`ticketStatuses`).
    <section data-role="desktop-working" aria-label="Active tickets">
      {/* `role="status"` is on the head line ONLY, not the whole panel: the
          section holds buttons, and wrapping interactive controls in a live
          region makes assistive tech re-announce them on every re-render. */}
      <div
        data-role="desktop-working-head"
        data-active={live ? 'true' : 'false'}
        data-state={headState}
        role={live ? 'status' : undefined}
      >
        {/* The head wears the SAME MARK as the rows under it — literally the
            shared `status-dot` from PrimaryScreen.css, keyed on the same two
            attributes, not a second dot of the panel's own.

            It used to be its own 6px element with its own fill and its own
            opacity breath. That matched the rows' tempo but nothing else: a
            slightly smaller mark, breathing a different property, with a halo
            only while live — three small disagreements in a column where the
            head and the first row sit eight pixels apart, which read as two
            different kinds of thing being reported rather than one reading
            summarising the others.

            The hue still comes from `data-state` — the ticket's lifecycle state,
            which is what keeps the summary honest: the head's ink is the ink of
            every row beneath it, because it is the same rule painting both. */}
        <span
          data-role="status-dot"
          data-state={headState}
          data-status={headStatus}
          aria-hidden="true"
        />
        <span>{workingPanelLabel(state)}</span>
      </div>
      {tickets.length > 0 ? (
        <ul data-role="desktop-working-list">
          {tickets.map((ticket) => {
            const note = activeStatusNote(ticket);
            return (
              <li key={ticket.id}>
                <button
                  type="button"
                  data-role="desktop-working-ticket"
                  // The ticket's own state, on the row as well as on its mark —
                  // the backlog's rows have always carried it, and it is what
                  // lets anything downstream (a rule, a test, a future
                  // affordance) address "the working rows" without inferring the
                  // state from a colour.
                  data-state={ticket.state}
                  data-status={ticket.status ?? undefined}
                  // Spelled out rather than left to the row's text content, so
                  // the bare "12m" becomes a sentence and the ticket's reading is
                  // stated even when the visible note is empty. The state is
                  // named too: fire and a word are the visible difference between
                  // a stuck row and a live one, and neither survives being read
                  // aloud.
                  aria-label={`Open ${ticket.state} ticket: ${ticket.title} — ${
                    note === '' ? 'working' : note
                  } ${agePhrase(ticket.since, now)}`}
                  onClick={() => {
                    onOpenTicket(ticket.id);
                  }}
                >
                  {/* The SAME mark the phone's ticket list uses — and the one
                      the head above wears too, all three from the same unscoped
                      rules in PrimaryScreen.css: the ticket's ember while it is
                      worked (breathing while a session builds, flat while it is
                      idle, hollow once it has stopped), the sheet's fire while it
                      is blocked, and fire too when a working session has failed.
                      Reusing the element rather than restating the palette here
                      is what keeps a ticket looking the same on both platforms; a
                      second set of colours would drift the moment either is
                      tuned.

                      `data-state` is the ticket's own, which is the same value
                      the head above takes and the same one the detail sheet's
                      badge is keyed on — so head, row, and sheet cannot disagree
                      about a ticket the user can see all three readings of. It
                      was the literal `working` while this list held only Working
                      tickets; a blocked row painted ember would have been that
                      disagreement, in the loudest place to have it.

                      `data-status` is absent for a blocked row rather than
                      falsified: see `ActiveTicket.status`. */}
                  <span
                    data-role="status-dot"
                    data-state={ticket.state}
                    data-status={ticket.status ?? undefined}
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
        // One line, not two. It used to have a second reading — "Nothing in
        // progress — a ticket needs you." — for the case where the absence had a
        // cause the head's one word could not carry. That case cannot reach this
        // branch any more: a blocked ticket is a row now, so a board with one in
        // it has a list rather than an absence to report, and the sentence would
        // be a stated absence directly above the thing it says is missing.
        <p data-role="desktop-working-empty">Nothing in progress.</p>
      )}
    </section>
  );
}
