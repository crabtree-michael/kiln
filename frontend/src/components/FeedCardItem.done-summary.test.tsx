// Component tests for the done card's work-summary body: a completion card leads
// with its GitHub link (the actionable jump to the landed commit/PR) and then
// surfaces the landed work's description — the full commit message / PR
// description — as an expand-in-place body below the link. A done card with no
// summary, and a poke card, stay body-less exactly as before.
import { cleanup, render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { FeedCardItem } from '@/components/FeedCardItem';
import { makeFeedCard } from '@/test/fixtures';

const NOW = Date.parse('2026-07-11T00:00:00Z');
const CREATED = '2026-07-11T00:00:00Z';

const noop = vi.fn<(ticketId: string) => void>();

function body(container: HTMLElement): HTMLElement | null {
  return container.querySelector('[data-role="feed-card-body"]');
}

/** The DOM-order list of data-role slots, so a test can assert relative order
 * (e.g. the summary body renders before the GitHub footer) without type-asserting
 * DOM nodes. */
function roleOrder(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll('[data-role]'))
    .map((el) => el.getAttribute('data-role'))
    .filter((role): role is string => role !== null);
}

afterEach(cleanup);

describe('FeedCardItem done card work summary', () => {
  it('renders the work summary as the card body', () => {
    const card = makeFeedCard({
      kind: 'done',
      id: 'update:1',
      label: 'Show a 404 page for unmatched routes',
      body: '',
      createdAt: CREATED,
      workSummary: 'feat(web): show a 404 page for unmatched routes',
    });
    const { container } = render(<FeedCardItem card={card} now={NOW} onAccept={noop} />);

    const el = body(container);
    expect(el).not.toBeNull();
    expect(el?.textContent).toBe('feat(web): show a 404 page for unmatched routes');
  });

  it('renders the GitHub link before the summary body', () => {
    const card = makeFeedCard({
      kind: 'done',
      id: 'update:1',
      label: 'Show a 404 page',
      body: '',
      createdAt: CREATED,
      workSummary: 'feat(web): show a 404 page',
      githubUrl: 'https://github.com/o/r/commit/a1b2c3d',
      githubLabel: 'a1b2c3d',
    });
    const { container } = render(<FeedCardItem card={card} now={NOW} onAccept={noop} />);

    // The GitHub link leads, with the description body below it: the actionable
    // jump comes first, the commit message follows.
    const order = roleOrder(container);
    expect(order).toContain('feed-card-body');
    expect(order).toContain('feed-card-github');
    expect(order.indexOf('feed-card-github')).toBeLessThan(order.indexOf('feed-card-body'));
  });

  it('renders a multi-line commit message verbatim as the body', () => {
    const message =
      'feat(web): show a 404 page\n\nAdds a catch-all route so unmatched paths render the 404 view.';
    const card = makeFeedCard({
      kind: 'done',
      id: 'update:1',
      label: 'Show a 404 page',
      body: '',
      createdAt: CREATED,
      workSummary: message,
      githubUrl: 'https://github.com/o/r/commit/a1b2c3d',
      githubLabel: 'a1b2c3d',
    });
    const { container } = render(<FeedCardItem card={card} now={NOW} onAccept={noop} />);

    // The whole message is preserved (subject + body) so tapping to expand reveals
    // the full text; CSS (white-space: pre-line) renders the line breaks.
    expect(body(container)?.textContent).toBe(message);
  });

  it('stays body-less when a done card has no work summary', () => {
    const card = makeFeedCard({
      kind: 'done',
      id: 'update:1',
      label: 'Ship it',
      body: '',
      createdAt: CREATED,
      githubUrl: 'https://github.com/o/r/commit/a1b2c3d',
      githubLabel: 'a1b2c3d',
    });
    const { container } = render(<FeedCardItem card={card} now={NOW} onAccept={noop} />);

    expect(body(container)).toBeNull();
    // The GitHub footer still renders on a summary-less done card.
    expect(container.querySelector('[data-role="feed-card-github"]')).not.toBeNull();
  });

  it('never gives a poke card a body', () => {
    const card = makeFeedCard({
      kind: 'poke',
      id: 'update:2',
      label: 'Nudge the agent',
      body: '',
      createdAt: CREATED,
    });
    const { container } = render(<FeedCardItem card={card} now={NOW} onAccept={noop} />);

    expect(body(container)).toBeNull();
  });
});
