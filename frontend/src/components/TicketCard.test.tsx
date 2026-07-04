// TicketCard image-snapshot target (07 §9): all five board states, plus a
// long `blocked_reason` shown in full (07 §7 — Blocked is the loudest
// surface on the page since push is deferred). DOM-structure snapshots stand
// in for true pixel snapshots (D4: no new dependency) — see the report for
// the deferral note.
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { TicketCard } from '@/components/TicketCard';
import { makeTicket, LONG_BLOCKED_REASON } from '@/test/fixtures';

const baseFields = {
  createdAt: '2026-07-01T00:00:00Z',
  updatedAt: '2026-07-01T00:00:00Z',
};

describe('TicketCard', () => {
  it('renders a shaping ticket with title and body preview', () => {
    render(
      <TicketCard
        ticket={makeTicket({
          ...baseFields,
          id: 't1',
          title: 'Add login page',
          body: 'Users need a way to sign in.',
          state: 'shaping',
          priority: 0,
        })}
      />,
    );

    expect(screen.getByText('Add login page')).toBeInTheDocument();
    expect(screen.getByText('Users need a way to sign in.')).toBeInTheDocument();
    expect(screen.getByRole('article')).toHaveAttribute('data-state', 'shaping');
  });

  it('renders a ready ticket', () => {
    render(
      <TicketCard
        ticket={makeTicket({
          ...baseFields,
          id: 't2',
          title: 'Ready ticket',
          body: 'Ready for pull.',
          state: 'ready',
          priority: 3,
          readyAt: '2026-07-02T00:00:00Z',
        })}
      />,
    );

    expect(screen.getByRole('article')).toHaveAttribute('data-state', 'ready');
    expect(screen.getByText('Ready ticket')).toBeInTheDocument();
  });

  it('renders a working ticket', () => {
    render(
      <TicketCard
        ticket={makeTicket({
          ...baseFields,
          id: 't3',
          title: 'Working ticket',
          body: 'A worker has it.',
          state: 'working',
          priority: 2,
        })}
      />,
    );

    expect(screen.getByRole('article')).toHaveAttribute('data-state', 'working');
  });

  it('renders a done ticket', () => {
    render(
      <TicketCard
        ticket={makeTicket({
          ...baseFields,
          id: 't4',
          title: 'Done ticket',
          body: 'Shipped.',
          state: 'done',
          priority: 1,
        })}
      />,
    );

    expect(screen.getByRole('article')).toHaveAttribute('data-state', 'done');
  });

  it('renders a blocked ticket with the full blocked_reason, not truncated (07 §7)', () => {
    render(
      <TicketCard
        ticket={makeTicket({
          ...baseFields,
          id: 't5',
          title: 'Blocked ticket',
          body: 'Waiting on input.',
          state: 'blocked',
          priority: 4,
          blockedReason: LONG_BLOCKED_REASON,
        })}
      />,
    );

    const article = screen.getByRole('article');
    expect(article).toHaveAttribute('data-state', 'blocked');
    const reasonNode = article.querySelector('[data-role="blocked-reason"]');
    expect(reasonNode).not.toBeNull();
    expect(reasonNode?.textContent).toBe(LONG_BLOCKED_REASON);
  });

  it('does not render a blocked-reason node for non-blocked states even if set defensively', () => {
    render(
      <TicketCard
        ticket={makeTicket({
          ...baseFields,
          id: 't6',
          title: 'Working with stray reason',
          body: 'Should not show reason.',
          state: 'working',
          priority: 0,
        })}
      />,
    );

    const article = screen.getByRole('article');
    expect(article.querySelector('[data-role="blocked-reason"]')).toBeNull();
  });

  it('matches the DOM-structure snapshot for a blocked ticket (07 §9 target)', () => {
    const { container } = render(
      <TicketCard
        ticket={makeTicket({
          ...baseFields,
          id: 't7',
          title: 'Snapshot blocked ticket',
          body: 'Waiting.',
          state: 'blocked',
          priority: 0,
          blockedReason: LONG_BLOCKED_REASON,
        })}
      />,
    );

    expect(container).toMatchSnapshot();
  });
});
