// The ticket sheet's dependency section (0013) — what this ticket is waiting
// for, by name.
//
// The board rows only have room for a count ("waiting on 2"), and that count is
// exactly what provokes "on what?". This is where that is answered, so these
// assert the list is legible: the titles rather than the ids, landed
// dependencies still listed (progress on a held ticket is otherwise invisible),
// and no section at all when the ticket waits for nothing.
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { TicketDetail } from '@/components/TicketDetail';
import { makeTicket } from '@/test/fixtures';

const at = '2026-08-08T10:00:00Z';

const waiting = makeTicket({
  id: 't-1',
  title: 'Use the new column',
  body: 'body',
  state: 'ready',
  priority: 0,
  createdAt: at,
  updatedAt: at,
  dependsOn: ['a', 'b'],
  unmetDependencies: 1,
});

describe('TicketDetail dependencies', () => {
  it('names what the ticket is waiting for, rather than leaving the user with a count', () => {
    render(
      <TicketDetail
        ticket={waiting}
        onClose={vi.fn()}
        dependencies={[
          { id: 'a', title: 'Land the migration', done: true },
          { id: 'b', title: 'Backfill the rows', done: false },
        ]}
      />,
    );

    expect(screen.getByText('Waiting on')).toBeInTheDocument();
    expect(screen.getByText('Land the migration')).toBeInTheDocument();
    expect(screen.getByText('Backfill the rows')).toBeInTheDocument();
  });

  // A finished dependency stays on the list and is marked done: "1 of 2 landed"
  // is the only progress a held ticket has, and dropping the finished ones would
  // make a nearly-unblocked ticket look identical to one that has not started.
  it('keeps landed dependencies listed, marked done', () => {
    render(
      <TicketDetail
        ticket={waiting}
        onClose={vi.fn()}
        dependencies={[
          { id: 'a', title: 'Land the migration', done: true },
          { id: 'b', title: 'Backfill the rows', done: false },
        ]}
      />,
    );

    const landed = screen.getByText('Land the migration');
    const outstanding = screen.getByText('Backfill the rows');
    expect(landed).toHaveAttribute('data-done', 'true');
    expect(outstanding).not.toHaveAttribute('data-done');
  });

  // Once nothing is outstanding the ticket is no longer *waiting* — it just has
  // a history of what it followed, and the label says so.
  it('reads "Depends on" once the ticket is no longer held', () => {
    const unblocked = makeTicket({
      id: 't-2',
      title: 'Free now',
      body: '',
      state: 'ready',
      priority: 0,
      createdAt: at,
      updatedAt: at,
      dependsOn: ['a'],
      unmetDependencies: 0,
    });
    render(
      <TicketDetail
        ticket={unblocked}
        onClose={vi.fn()}
        dependencies={[{ id: 'a', title: 'Land the migration', done: true }]}
      />,
    );

    expect(screen.getByText('Depends on')).toBeInTheDocument();
    expect(screen.queryByText('Waiting on')).toBeNull();
  });

  it('renders no section for a ticket that waits for nothing', () => {
    const plain = makeTicket({
      id: 't-3',
      title: 'Plain',
      body: '',
      state: 'ready',
      priority: 0,
      createdAt: at,
      updatedAt: at,
    });
    render(<TicketDetail ticket={plain} onClose={vi.fn()} />);

    expect(screen.queryByText('Waiting on')).toBeNull();
    expect(screen.queryByText('Depends on')).toBeNull();
  });
});
