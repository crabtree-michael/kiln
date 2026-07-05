// Header status dropdown tests (08 §2): the collapsed summary stays put, and
// the panel breaks the streams out per-agent — building first, then idle — with
// toggle / outside-click / Escape dismissal.
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
    const trigger = screen.getByText('3 streams');
    expect(trigger).toHaveAttribute('data-role', 'feed-status');
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByRole('button')).toHaveAttribute('aria-expanded', 'false');
  });

  it('opens on click and lists each stream broken out per-agent (building first)', () => {
    render(<HeaderStatusMenu summary={summary} board={board} />);
    fireEvent.click(screen.getByRole('button'));
    expect(screen.getByRole('button')).toHaveAttribute('aria-expanded', 'true');

    const rows = screen.getAllByRole('listitem');
    expect(rows).toHaveLength(3);
    // Building streams (working) come first, then idle (blocked).
    expect(rows[0]).toHaveAttribute('data-status', 'building');
    expect(within(rows[0]!).getByText('Auth')).toBeInTheDocument();
    expect(within(rows[0]!).getByText('Building')).toBeInTheDocument();
    expect(rows[2]).toHaveAttribute('data-status', 'idle');
    expect(within(rows[2]!).getByText('Billing')).toBeInTheDocument();
    expect(within(rows[2]!).getByText('Idle')).toBeInTheDocument();
    // The blocker reason rides along on the idle row.
    expect(screen.getByText('Which gateway should we bill through?')).toBeInTheDocument();
  });

  it('shows each stream real session status from board.agents, overriding the column default', () => {
    // The Streams view must reflect the actual agent session state, not a
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
    // w1's dead sandbox is visibly distinct from a building one.
    expect(rows[0]).toHaveAttribute('data-status', 'stopped');
    expect(within(rows[0]!).getByText('Stopped')).toBeInTheDocument();
    expect(rows[1]).toHaveAttribute('data-status', 'building');
    expect(within(rows[1]!).getByText('Building')).toBeInTheDocument();
    // b1 has no agent entry, so it falls back to the blocked-column default.
    expect(rows[2]).toHaveAttribute('data-status', 'idle');
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
    fireEvent.click(screen.getByText('3 streams'));
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

  it('shows an empty affordance when there are no active streams', () => {
    render(<HeaderStatusMenu summary={makeFeedSummary()} board={makeBoard()} />);
    // The collapsed trigger reads "Nothing active" rather than a count at zero.
    expect(screen.getByText('Nothing active')).toHaveAttribute('data-role', 'feed-status');
    fireEvent.click(screen.getByRole('button'));
    expect(screen.getByText('No active streams')).toHaveAttribute(
      'data-role',
      'header-status-empty',
    );
    expect(screen.queryAllByRole('listitem')).toHaveLength(0);
  });

  it('treats a null board as no active streams (pre-first-snapshot)', () => {
    render(<HeaderStatusMenu summary={makeFeedSummary({ stream_count: 2 })} board={null} />);
    fireEvent.click(screen.getByRole('button'));
    expect(screen.getByText('No active streams')).toBeInTheDocument();
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
      screen.getByText('Loading streams…').closest('[data-role="header-status-loading"]'),
    ).not.toBeNull();
    // The loading state is distinct from the genuinely-empty affordance.
    expect(screen.queryByText('No active streams')).not.toBeInTheDocument();
  });

  it('keeps showing streams while a background refresh is in flight', () => {
    render(<HeaderStatusMenu summary={summary} board={board} refreshing />);
    fireEvent.click(screen.getByRole('button'));
    // A refresh over already-loaded streams doesn't blank the list.
    expect(screen.queryByText('Loading streams…')).not.toBeInTheDocument();
    expect(screen.getAllByRole('listitem')).toHaveLength(3);
  });
});
