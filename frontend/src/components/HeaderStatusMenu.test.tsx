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
    render(
      <HeaderStatusMenu summary={summary} board={board} onSelectTicket={onSelectTicket} />,
    );
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
    render(
      <HeaderStatusMenu summary={summary} board={board} onSelectTicket={onSelectTicket} />,
    );
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
