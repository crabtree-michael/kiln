// Header status dropdown tests (08 §2): the collapsed summary stays put, and
// the panel breaks the streams out per-agent — building first, then idle — with
// toggle / outside-click / Escape dismissal.
import { describe, expect, it } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { HeaderStatusMenu } from '@/components/HeaderStatusMenu';
import { makeBoard, makeFeedSummary, makeTicket } from '@/test/fixtures';

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
    const trigger = screen.getByText('3 streams · nothing needs you');
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
    fireEvent.click(screen.getByText('3 streams · nothing needs you'));
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
});
