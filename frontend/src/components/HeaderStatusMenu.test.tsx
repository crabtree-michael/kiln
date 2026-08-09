// Header status dropdown tests (08 §2): the collapsed summary stays put, and
// the panel lists every ticket — active first (working then blocked), each row's
// chip its worker's session status — with toggle / outside-click / Escape dismissal.
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { HeaderStatusMenu } from '@/components/HeaderStatusMenu';
import { makeAgentStatus, makeBoard, makeFeedSummary, makeTicket } from '@/test/fixtures';

const baseFields = { createdAt: '2026-07-01T00:00:00Z', updatedAt: '2026-07-01T00:00:00Z' };

const board = makeBoard({
  working: [
    makeTicket({ ...baseFields, id: 'w1', title: 'Auth', body: '', state: 'working', priority: 0 }),
    makeTicket({
      ...baseFields,
      id: 'w2',
      title: 'Search',
      body: '',
      state: 'working',
      priority: 0,
    }),
  ],
  blocked: [
    makeTicket({
      ...baseFields,
      id: 'b1',
      title: 'Billing',
      body: '',
      state: 'blocked',
      priority: 0,
      blockedReason: 'Which gateway should we bill through?',
    }),
  ],
});

const summary = makeFeedSummary({ stream_count: 3, building: 2, idle: 1 });

describe('HeaderStatusMenu', () => {
  it('keeps the collapsed summary text and starts closed', () => {
    render(<HeaderStatusMenu summary={summary} board={board} />);
    const trigger = screen.getByText('3 tickets');
    expect(trigger).toHaveAttribute('data-role', 'feed-status');
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByRole('button')).toHaveAttribute('aria-expanded', 'false');
  });

  it('counts queued (ready) tickets in the collapsed badge, matching the dropdown', () => {
    // The badge reflects everything the panel lists — active plus queued — not
    // just the active stream_count. Here two active + two ready = four, even
    // though the summary's stream_count only tracks the two active streams.
    const withQueue = makeBoard({
      working: [
        makeTicket({
          ...baseFields,
          id: 'w1',
          title: 'Auth',
          body: '',
          state: 'working',
          priority: 0,
        }),
      ],
      blocked: [
        makeTicket({
          ...baseFields,
          id: 'b1',
          title: 'Billing',
          body: '',
          state: 'blocked',
          priority: 0,
        }),
      ],
      ready: [
        makeTicket({
          ...baseFields,
          id: 'r1',
          title: 'Export',
          body: '',
          state: 'ready',
          priority: 0,
        }),
        makeTicket({
          ...baseFields,
          id: 'r2',
          title: 'Import',
          body: '',
          state: 'ready',
          priority: 0,
        }),
      ],
    });
    render(<HeaderStatusMenu summary={makeFeedSummary({ stream_count: 2 })} board={withQueue} />);
    // Badge counts all four, not the stream_count of two.
    expect(screen.getByText('4 tickets')).toHaveAttribute('data-role', 'feed-status');
    // …and it matches the number of rows the dropdown renders.
    fireEvent.click(screen.getByRole('button'));
    expect(screen.getAllByRole('listitem')).toHaveLength(4);
  });

  it('opens on click and lists each ticket broken out per-agent (working first)', () => {
    render(<HeaderStatusMenu summary={summary} board={board} />);
    fireEvent.click(screen.getByRole('button'));
    expect(screen.getByRole('button')).toHaveAttribute('aria-expanded', 'true');

    const rows = screen.getAllByRole('listitem');
    expect(rows).toHaveLength(3);
    // Working tickets come first, then blocked.
    expect(rows[0]).toHaveAttribute('data-status', 'building');
    expect(within(rows[0]!).getByText('Auth')).toBeInTheDocument();
    expect(rows[2]).toHaveAttribute('data-status', 'idle');
    expect(within(rows[2]!).getByText('Billing')).toBeInTheDocument();
    // The blocker reason rides along on the idle row.
    expect(screen.getByText('Which gateway should we bill through?')).toBeInTheDocument();
  });

  it('shows each ticket real session status from board.agents, overriding the column default', () => {
    // The ticket view must reflect the actual agent session state, not a
    // hardcoded "building" derived from the board column (amended 2026-07-05):
    // w1 has silently stopped, w2 is genuinely building, and b1 (blocked, no
    // agent entry) falls back to idle.
    const withAgents = makeBoard({
      working: [
        makeTicket({
          ...baseFields,
          id: 'w1',
          title: 'Auth',
          body: '',
          state: 'working',
          priority: 0,
        }),
        makeTicket({
          ...baseFields,
          id: 'w2',
          title: 'Search',
          body: '',
          state: 'working',
          priority: 0,
        }),
      ],
      blocked: [
        makeTicket({
          ...baseFields,
          id: 'b1',
          title: 'Billing',
          body: '',
          state: 'blocked',
          priority: 0,
        }),
      ],
      agents: [makeAgentStatus('w1', 'stopped'), makeAgentStatus('w2', 'building')],
    });
    render(<HeaderStatusMenu summary={summary} board={withAgents} />);
    fireEvent.click(screen.getByRole('button'));

    const rows = screen.getAllByRole('listitem');
    expect(rows).toHaveLength(3);
    // w1's dead sandbox is visibly distinct from a building one — the dot on
    // its row carries the state now that the text label is gone.
    expect(rows[0]).toHaveAttribute('data-status', 'stopped');
    expect(rows[1]).toHaveAttribute('data-status', 'building');
    // b1 has no agent entry, so it falls back to the blocked-column default.
    expect(rows[2]).toHaveAttribute('data-status', 'idle');
  });

  it('marks each row with the ticket’s own state, so the row and its sheet agree', () => {
    // The mark's colour comes from `data-state` (the TICKET) and its texture
    // from `data-status` (the session) — see the shared status mark in
    // PrimaryScreen.css, which is where the shared mark's tokens live. Without the
    // state on the dot a working ticket took the accent from `building` and read
    // as blocked, contradicting the detail sheet the row opens.
    render(<HeaderStatusMenu summary={summary} board={board} />);
    fireEvent.click(screen.getByRole('button'));

    const dots = screen
      .getAllByRole('listitem')
      .map((row) => row.querySelector<HTMLElement>('[data-role="status-dot"]'));
    expect(dots.map((dot) => dot?.dataset.state)).toEqual(['working', 'working', 'blocked']);
    // Both attributes ride on the dot itself, so any list can render the whole
    // vocabulary without restating a rule.
    expect(dots.map((dot) => dot?.dataset.status)).toEqual(['building', 'building', 'idle']);
  });

  // The head over the list, which used to be the bare word "Tickets" while the
  // desk's in-progress head had worn its rows' own mark for three fixes. These
  // pin the two halves of "the same exact one": the same ELEMENT (so size, ink,
  // texture and cadence are the one rule that paints the rows), keyed on the TOP
  // ticket (so the head cannot disagree with the row directly beneath it).
  const headingDot = (): HTMLElement | null =>
    screen
      .getByText('Tickets')
      .closest('[data-role="header-status-heading"]')
      ?.querySelector<HTMLElement>('[data-role="status-dot"]') ?? null;

  it('heads the list with the shared status mark of the top ticket', () => {
    render(<HeaderStatusMenu summary={summary} board={board} />);
    fireEvent.click(screen.getByRole('button'));

    const head = headingDot();
    expect(head).not.toBeNull();
    // The first row is w1 (working, building), so that is what the head says.
    const first = screen
      .getAllByRole('listitem')[0]
      ?.querySelector<HTMLElement>('[data-role="status-dot"]');
    expect(head?.dataset.state).toBe(first?.dataset.state);
    expect(head?.dataset.status).toBe(first?.dataset.status);
    expect(head?.dataset.state).toBe('working');
    expect(head?.dataset.status).toBe('building');
    // Decoration: the head's word is what is read out, not a second status.
    expect(head).toHaveAttribute('aria-hidden', 'true');
  });

  it('follows the top ticket when the loudest thing on the board changes', () => {
    // With nothing working, `ticketStatuses` puts the blocked ticket first — and
    // the head is fire, because the head is that row.
    const blockedFirst = makeBoard({
      blocked: [
        makeTicket({
          ...baseFields,
          id: 'b1',
          title: 'Billing',
          body: '',
          state: 'blocked',
          priority: 0,
        }),
      ],
      ready: [
        makeTicket({
          ...baseFields,
          id: 'r1',
          title: 'Export',
          body: '',
          state: 'ready',
          priority: 0,
        }),
      ],
    });
    render(<HeaderStatusMenu summary={summary} board={blockedFirst} />);
    fireEvent.click(screen.getByRole('button'));

    expect(headingDot()?.dataset.state).toBe('blocked');
    expect(headingDot()?.dataset.status).toBe('idle');
  });

  it('leaves the head stateless when there is no ticket to report', () => {
    // An empty board and a board still loading both land here. The mark stays —
    // the heading's geometry must not shift when the first ticket arrives under
    // it — and falls back to the shared mark's flat, faint default rather than
    // claiming a state nothing on the board is in.
    render(<HeaderStatusMenu summary={makeFeedSummary()} board={makeBoard()} />);
    fireEvent.click(screen.getByRole('button'));

    const head = headingDot();
    expect(head).not.toBeNull();
    expect(head?.dataset.state).toBeUndefined();
    expect(head?.dataset.status).toBeUndefined();
  });

  it('renders a compact time-in-status age subtext on every ticket row', () => {
    render(<HeaderStatusMenu summary={summary} board={board} />);
    fireEvent.click(screen.getByRole('button'));

    const rows = screen.getAllByRole('listitem');
    for (const row of rows) {
      const age = row.querySelector('[data-role="header-status-age"]');
      expect(age).not.toBeNull();
      // Compact relative age — "now", "10m", "2h", "1d" — never empty.
      expect(age?.textContent).toMatch(/^(now|\d+[mhd])$/);
    }
  });

  it('toggles closed on a second click', () => {
    render(<HeaderStatusMenu summary={summary} board={board} />);
    const button = screen.getByRole('button');
    fireEvent.click(button);
    expect(button).toHaveAttribute('aria-expanded', 'true');
    fireEvent.click(button);
    expect(button).toHaveAttribute('aria-expanded', 'false');
  });

  it('dismisses on an outside click', () => {
    render(
      <div>
        <HeaderStatusMenu summary={summary} board={board} />
        <button type="button">outside</button>
      </div>,
    );
    fireEvent.click(screen.getByText('3 tickets'));
    const [trigger] = screen.getAllByRole('button');
    expect(trigger).toHaveAttribute('aria-expanded', 'true');
    fireEvent.mouseDown(screen.getByText('outside'));
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
  });

  it('dismisses on Escape', () => {
    render(<HeaderStatusMenu summary={summary} board={board} />);
    const button = screen.getByRole('button');
    fireEvent.click(button);
    expect(button).toHaveAttribute('aria-expanded', 'true');
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(button).toHaveAttribute('aria-expanded', 'false');
  });

  it('shows an empty affordance when the board has no tickets', () => {
    render(<HeaderStatusMenu summary={makeFeedSummary()} board={makeBoard()} />);
    // The collapsed trigger reads "Nothing active" rather than a count at zero.
    expect(screen.getByText('Nothing active')).toHaveAttribute('data-role', 'feed-status');
    fireEvent.click(screen.getByRole('button'));
    expect(screen.getByText('No tickets')).toHaveAttribute('data-role', 'header-status-empty');
    expect(screen.queryAllByRole('listitem')).toHaveLength(0);
  });

  it('treats a null board as no tickets (pre-first-snapshot)', () => {
    render(<HeaderStatusMenu summary={makeFeedSummary({ stream_count: 2 })} board={null} />);
    fireEvent.click(screen.getByRole('button'));
    expect(screen.getByText('No tickets')).toBeInTheDocument();
  });

  it('fires onOpen when opening, but not when closing', () => {
    const onOpen = vi.fn();
    render(<HeaderStatusMenu summary={summary} board={board} onOpen={onOpen} />);
    const button = screen.getByRole('button');

    fireEvent.click(button); // closed → open: fetch fresh state
    expect(onOpen).toHaveBeenCalledTimes(1);

    fireEvent.click(button); // open → closed: no fetch
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it('shows a loading indicator instead of the empty state while refreshing with nothing yet', () => {
    render(
      <HeaderStatusMenu summary={makeFeedSummary({ stream_count: 2 })} board={null} refreshing />,
    );
    fireEvent.click(screen.getByRole('button'));
    expect(
      screen.getByText('Loading tickets…').closest('[data-role="header-status-loading"]'),
    ).not.toBeNull();
    // The loading state is distinct from the genuinely-empty affordance.
    expect(screen.queryByText('No tickets')).not.toBeInTheDocument();
  });

  it('keeps showing tickets while a background refresh is in flight', () => {
    render(<HeaderStatusMenu summary={summary} board={board} refreshing />);
    fireEvent.click(screen.getByRole('button'));
    // A refresh over an already-loaded list doesn't blank it.
    expect(screen.queryByText('Loading tickets…')).not.toBeInTheDocument();
    expect(screen.getAllByRole('listitem')).toHaveLength(3);
  });

  it('makes each row select its ticket (and dismiss the menu) when onSelectTicket is wired', () => {
    const onSelectTicket = vi.fn();
    render(<HeaderStatusMenu summary={summary} board={board} onSelectTicket={onSelectTicket} />);
    const trigger = screen.getByRole('button', { name: /3 tickets/i });
    fireEvent.click(trigger);

    // With a select handler the rows become buttons; the first is w1 (Auth).
    const row = screen.getByRole('button', { name: 'Open ticket: Auth' });
    fireEvent.click(row);
    expect(onSelectTicket).toHaveBeenCalledWith('w1');
    // Selecting a ticket closes the dropdown so the detail overlay is unobscured.
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
  });

  it('selects the ticket on Enter/Space so rows are keyboard-actionable', () => {
    const onSelectTicket = vi.fn();
    render(<HeaderStatusMenu summary={summary} board={board} onSelectTicket={onSelectTicket} />);
    fireEvent.click(screen.getByRole('button', { name: /3 tickets/i }));

    const row = screen.getByRole('button', { name: 'Open ticket: Billing' });
    fireEvent.keyDown(row, { key: 'Enter' });
    expect(onSelectTicket).toHaveBeenNthCalledWith(1, 'b1');
    fireEvent.keyDown(row, { key: ' ' });
    expect(onSelectTicket).toHaveBeenNthCalledWith(2, 'b1');
  });

  it('leaves rows presentational (non-interactive) when onSelectTicket is omitted', () => {
    render(<HeaderStatusMenu summary={summary} board={board} />);
    fireEvent.click(screen.getByRole('button'));
    // Without a handler the rows stay plain list items — no button role, no
    // interactive affordance — so purely presentational renders are unchanged.
    expect(screen.getAllByRole('listitem')).toHaveLength(3);
    expect(screen.queryByRole('button', { name: /^Open ticket:/ })).not.toBeInTheDocument();
  });
});

// The dependency label's place on the row. It first shipped as a span of its own
// with no rule to its name, which the row's grid auto-placed onto a third line
// at the row's own size and ink — a fact floating under the ticket rather than a
// qualifier on the time it belongs to. What these assert is that it is now part
// of the one subtitle line, ahead of the age, in the age's own styling.
describe('HeaderStatusMenu — the dependency subtitle', () => {
  // The row reads the real clock (`relativeAge` defaults to `Date.now()`), so
  // the fixture is dated from it: three hours before whenever this runs is "3h"
  // however long the suite takes to get here. No fake timers — the component
  // has none of its own, and pinning the clock for a pure formatting assertion
  // would be a heavier tool than the claim needs.
  const threeHoursAgo = () => new Date(Date.now() - 3 * 60 * 60 * 1000).toISOString();

  const queuedBoard = (dependsOn: string[]) =>
    makeBoard({
      ready: [
        makeTicket({
          ...baseFields,
          id: 'r1',
          title: 'Ship the column',
          body: '',
          state: 'ready',
          priority: 0,
          statusChangedAt: threeHoursAgo(),
          dependsOn,
        }),
      ],
    });

  const openPanel = (dependsOn: string[]) => {
    render(<HeaderStatusMenu summary={summary} board={queuedBoard(dependsOn)} />);
    fireEvent.click(screen.getByRole('button'));
    const row = screen.getByText('Ship the column').closest('li');
    if (row === null) {
      throw new Error('expected the queued ticket to have a row');
    }
    return row;
  };

  it('states the dependency, the dot and the age as one line, in that order', () => {
    const subtitle = within(openPanel(['b1', 'b2'])).getByText(/Waiting on/);
    expect(subtitle).toHaveTextContent('Waiting on 2 tickets · 3h');
  });

  it('puts that line in the age element, so it inherits the subtext styling', () => {
    // The whole fix: one subtitle element, styled by the existing
    // `header-status-age` rule (11px, --text-faint). A second span here is the
    // bug — it had no rule of its own and landed on its own line.
    const row = openPanel(['b1', 'b2']);
    const subtitle = within(row).getByText(/Waiting on/);
    expect(subtitle).toHaveAttribute('data-role', 'header-status-age');
    expect(row.querySelectorAll("[data-role='header-status-age']")).toHaveLength(1);
    expect(row.querySelector("[data-role='header-status-waiting']")).toBeNull();
  });

  it('leaves a ticket that waits on nothing with the bare age it always had', () => {
    const row = openPanel([]);
    expect(within(row).getByText('3h')).toHaveAttribute('data-role', 'header-status-age');
    expect(within(row).queryByText(/Waiting on/)).not.toBeInTheDocument();
  });
});
