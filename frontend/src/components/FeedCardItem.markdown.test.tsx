// Component tests for the feed card body's Markdown rendering. The brain writes
// its feed notes and ticket bodies in Markdown (06 prompt), so an update card —
// the summary of what happened on a ticket — must render headings, emphasis and
// lists as formatting rather than as literal `##`/`**`/`-` syntax. One component
// serves both shells (13), so this covers desktop and mobile at once.
//
// Two bodies deliberately stay verbatim text and are pinned here so they can't
// drift into the renderer: the done card's work summary (a commit message / PR
// description, whose hard-wrapped lines Markdown would fold into one run) and
// the proposal digest (which lives inside the click-through button, where block
// elements and nested links cannot go — the full ticket renders as Markdown in
// the detail sheet one tap away).
import { cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { FeedCardItem } from '@/components/FeedCardItem';
import { makeFeedCard } from '@/test/fixtures';

const NOW = Date.parse('2026-08-06T00:00:00Z');
const CREATED = '2026-08-06T00:00:00Z';

const noop = vi.fn<(ticketId: string) => void>();

function body(container: HTMLElement): HTMLElement {
  const el = container.querySelector('[data-role="feed-card-body"]');
  if (!(el instanceof HTMLElement)) {
    throw new Error('expected a feed card body');
  }
  return el;
}

/** jsdom performs no layout, so the clamp measurement has to be faked for the
 * body to become the expand toggle (mirrors PrimaryScreenView's helper). */
function fakeClampedOverflow(): void {
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(200);
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(80);
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('FeedCardItem body Markdown', () => {
  it('renders a ticket-linked update summary as formatted Markdown', () => {
    const card = makeFeedCard({
      kind: 'update',
      id: 'update:7',
      label: 'Login Redesign',
      body: '## What changed\n\nThe handshake now **retries**, so:\n\n- fewer timeouts\n- one round trip',
      ticketId: 't-login',
      notificationId: 7,
      createdAt: CREATED,
    });
    const { container } = render(<FeedCardItem card={card} now={NOW} onAccept={noop} />);

    const el = body(container);
    expect(el.querySelector('h2')?.textContent).toBe('What changed');
    expect(el.querySelector('strong')?.textContent).toBe('retries');
    expect(el.querySelectorAll('li')).toHaveLength(2);
    // The syntax itself is gone — this is the whole point of the card.
    expect(el.textContent).not.toContain('##');
    expect(el.textContent).not.toContain('**');
  });

  it('renders a blocker and a preview body as Markdown too', () => {
    // Every brain-authored body goes through the same renderer; only the done
    // card's commit message and the proposal digest opt out.
    for (const kind of ['blocker', 'preview'] as const) {
      const card = makeFeedCard({
        kind,
        id: `${kind}:1`,
        label: 'Login Redesign',
        body: 'Needs a *decision* on the copy.',
        ticketId: 't-login',
        createdAt: CREATED,
      });
      const { container } = render(<FeedCardItem card={card} now={NOW} onAccept={noop} />);
      expect(body(container).querySelector('em')?.textContent).toBe('decision');
      cleanup();
    }
  });

  it('renders no raw HTML from a body (react-markdown escapes it)', () => {
    const card = makeFeedCard({
      kind: 'update',
      id: 'update:8',
      label: 'Login Redesign',
      body: 'Careful: <img src="x" onerror="alert(1)"> is user-ish text.',
      notificationId: 8,
      createdAt: CREATED,
    });
    const { container } = render(<FeedCardItem card={card} now={NOW} onAccept={noop} />);

    const el = body(container);
    expect(el.querySelector('img')).toBeNull();
    expect(el.textContent).toContain('<img src="x" onerror="alert(1)">');
  });

  it('leaves a link in the body a link — tapping it does not expand the card', () => {
    fakeClampedOverflow();
    const card = makeFeedCard({
      kind: 'update',
      id: 'update:9',
      label: 'Login Redesign',
      body: 'See [the run](https://example.com/run) for the failing case, and much more besides.',
      notificationId: 9,
      createdAt: CREATED,
    });
    const { container } = render(<FeedCardItem card={card} now={NOW} onAccept={noop} />);

    const el = body(container);
    const link = el.querySelector('a');
    expect(link?.getAttribute('href')).toBe('https://example.com/run');

    // The clamped body is the expand toggle, but a press on the link is a press
    // on the link: following a reference is not a request to expand the note.
    expect(el).toHaveAttribute('data-clickable', 'true');
    if (!(link instanceof HTMLElement)) {
      throw new Error('expected the body link');
    }
    // jsdom can't navigate, and an unhandled attempt is logged as an error —
    // swallow the default so the click still reaches the guard under test.
    const swallowNavigation = (event: Event): void => {
      event.preventDefault();
    };
    document.addEventListener('click', swallowNavigation);
    fireEvent.click(link);
    document.removeEventListener('click', swallowNavigation);
    expect(el).not.toHaveAttribute('data-expanded');

    // A press on the prose still expands, exactly as before.
    fireEvent.click(el);
    expect(el).toHaveAttribute('data-expanded', 'true');
  });

  it('keeps the done card work summary verbatim, not Markdown', () => {
    const message = 'feat(web): show a 404 page\n\n* not a list marker\nWrapped continuation.';
    const card = makeFeedCard({
      kind: 'done',
      id: 'update:10',
      label: 'Show a 404 page',
      body: '',
      createdAt: CREATED,
      workSummary: message,
    });
    const { container } = render(<FeedCardItem card={card} now={NOW} onAccept={noop} />);

    const el = body(container);
    // Character-for-character, line breaks included (CSS white-space: pre-line
    // renders them) — no paragraphs, no list.
    expect(el.textContent).toBe(message);
    expect(el.querySelector('li')).toBeNull();
    expect(el.querySelector('p')).toBeNull();
  });

  it('keeps the proposal digest inside its click-through button as plain text', () => {
    const card = makeFeedCard({
      kind: 'proposal',
      id: 'proposal:t-login',
      label: 'Login Redesign',
      body: '## Objective\n\nRework the login screen.',
      ticketId: 't-login',
      createdAt: CREATED,
    });
    const { container } = render(
      <FeedCardItem card={card} now={NOW} onAccept={noop} onOpenDetail={noop} />,
    );

    // Block elements cannot live inside the button that opens the ticket, so the
    // digest stays text; the sheet behind it renders the same body as Markdown.
    const open = container.querySelector('[data-role="feed-card-open"]');
    expect(open?.tagName).toBe('BUTTON');
    expect(body(container).querySelector('h2')).toBeNull();
  });
});
