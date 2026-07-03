// BoardColumn image-snapshot target (07 §9): zone stacking within a column.
// The caller decides zone order; this component must render it top-to-bottom
// unchanged — the load-bearing case is Developing stacking Blocked *above*
// Working (01 §5, 07 §7).
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { BoardColumn, type BoardColumnZone } from '@/components/BoardColumn';
import { makeTicket } from '@/test/fixtures';

const baseFields = { createdAt: '2026-07-01T00:00:00Z', updatedAt: '2026-07-01T00:00:00Z' };

describe('BoardColumn', () => {
  it('renders zones top-to-bottom in the order given by the caller', () => {
    const zones: BoardColumnZone[] = [
      { label: 'Blocked', tickets: [], emphasis: 'loud' },
      { label: 'Working', tickets: [] },
    ];

    render(<BoardColumn title="Developing" zones={zones} />);

    const headings = screen.getAllByRole('heading', { level: 3 }).map((node) => node.textContent);
    expect(headings).toEqual(['Blocked', 'Working']);
  });

  it('marks the Blocked zone as loud emphasis (07 §7 — loudest surface)', () => {
    const zones: BoardColumnZone[] = [
      { label: 'Blocked', tickets: [], emphasis: 'loud' },
      { label: 'Working', tickets: [] },
    ];

    render(<BoardColumn title="Developing" zones={zones} />);

    const column = screen.getByRole('region', { name: 'Developing' });
    const zoneNodes = column.querySelectorAll('[data-role="board-zone"]');
    expect(zoneNodes).toHaveLength(2);
    expect(zoneNodes[0]?.getAttribute('data-emphasis')).toBe('loud');
    expect(zoneNodes[1]?.getAttribute('data-emphasis')).toBe('default');
  });

  it('renders each zone ticket list in the exact order supplied (e.g. Ready pull order)', () => {
    const tickets = [
      makeTicket({
        ...baseFields,
        id: 'r3',
        title: 'third',
        body: '',
        state: 'ready',
        priority: 1,
      }),
      makeTicket({
        ...baseFields,
        id: 'r1',
        title: 'first',
        body: '',
        state: 'ready',
        priority: 9,
      }),
      makeTicket({
        ...baseFields,
        id: 'r2',
        title: 'second',
        body: '',
        state: 'ready',
        priority: 5,
      }),
    ];
    const zones: BoardColumnZone[] = [{ label: 'Ready', tickets }];

    render(<BoardColumn title="Backlog" zones={zones} />);

    const titles = screen.getAllByRole('heading', { level: 3 })[0];
    expect(titles?.textContent).toBe('Ready');
    const cardTitles = screen
      .getAllByRole('article')
      .map((article) => article.querySelector('h3')?.textContent);
    expect(cardTitles).toEqual(['third', 'first', 'second']);
  });

  it('matches the DOM-structure snapshot for Developing (Blocked above Working) (07 §9 target)', () => {
    const zones: BoardColumnZone[] = [
      {
        label: 'Blocked',
        emphasis: 'loud',
        tickets: [
          makeTicket({
            ...baseFields,
            id: 'b1',
            title: 'Blocked ticket',
            body: 'stuck',
            state: 'blocked',
            priority: 0,
            blockedReason: 'needs a decision',
          }),
        ],
      },
      {
        label: 'Working',
        tickets: [
          makeTicket({
            ...baseFields,
            id: 'w1',
            title: 'Working ticket',
            body: 'in progress',
            state: 'working',
            priority: 0,
          }),
        ],
      },
    ];

    const { container } = render(<BoardColumn title="Developing" zones={zones} />);

    expect(container).toMatchSnapshot();
  });
});
