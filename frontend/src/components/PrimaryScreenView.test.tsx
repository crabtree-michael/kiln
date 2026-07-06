// Primary-screen view tests (08 §F image-snapshot targets + the Accept seam).
// DOM-structure snapshots stand in for pixel snapshots (same deferral as
// TicketCard/ChatPanel, 07 §9 D4). A fixed `now` is threaded through so the
// relative-age labels ("now", "2m", "1h") are deterministic across runs.
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { PrimaryScreenView } from '@/components/PrimaryScreenView';
import { makeBoard, makeFeedCard, makeFeedSnapshot, makeTicket } from '@/test/fixtures';
import { acceptTicket } from '@/transport/transport';

/**
 * jsdom performs no layout, so a card body's `scrollHeight`/`clientHeight` are
 * both 0 and the three-line clamp never registers as overflowing. Fake a clamped
 * box (content taller than the window) so the tap-body-to-expand affordance can
 * be exercised; restore after each test so the layout-free default holds for the
 * snapshot cases (mirrors ActivityRow.test's `fakeClampedOverflow`).
 */
function fakeClampedOverflow(): void {
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(200);
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(80);
}

vi.mock('@/transport/transport', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/transport/transport')>();
  return { ...actual, acceptTicket: vi.fn() };
});

// The dock is a live voice-store consumer (09). These presentational tests
// render `PrimaryScreenView` directly (no `VoiceProvider`), so `useVoice` is
// mocked to a static resting state — deterministic, and no mic/socket I/O. The
// dock's own state rendering is covered by Dock.test.tsx / Dock.snapshot.test.tsx.
vi.mock('@/voice/voice-context', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/voice/voice-context')>();
  return {
    ...actual,
    useVoice: () => ({
      micState: 'listening' as const,
      settledText: '',
      tailText: '',
      pause: vi.fn(),
      resume: vi.fn(),
      cancel: vi.fn(),
      sendNow: vi.fn(),
      getLevel: vi.fn(() => 0),
      keyboardMode: false,
      openKeyboard: vi.fn(),
      closeKeyboard: vi.fn(),
      submitText: vi.fn(() => Promise.resolve(true)),
    }),
  };
});

const NOW = new Date('2026-07-04T10:00:00Z').getTime();
const minutesAgo = (m: number): string => new Date(NOW - m * 60_000).toISOString();

const noop = (): void => {
  /* inert callback for presentational render tests */
};

const blockerCard = makeFeedCard({
  kind: 'blocker',
  id: 'blocker:t-auth',
  label: 'Auth',
  body: 'The /auth endpoint hands back both a session cookie and a JWT. Which should the client trust as the source of truth?',
  ticketId: 't-auth',
  createdAt: minutesAgo(0),
});

const rateLimitUpdate = makeFeedCard({
  kind: 'update',
  id: 'update:30',
  label: 'Rate limiting',
  body: 'Added a fixed-window limiter on /auth — 20 requests a minute per IP. Suite is green.',
  notificationId: 30,
  createdAt: minutesAgo(2),
});

const updateCards = [
  rateLimitUpdate,
  makeFeedCard({
    kind: 'update',
    id: 'update:20',
    label: 'Email',
    body: 'Drafted three subject lines for the password-reset email and kept the clearest.',
    notificationId: 20,
    createdAt: minutesAgo(14),
  }),
  makeFeedCard({
    kind: 'update',
    id: 'update:10',
    label: 'Search',
    body: 'Reindexed the product catalog; queries are back under 40ms.',
    notificationId: 10,
    createdAt: minutesAgo(60),
  }),
];

type ViewProps = Parameters<typeof PrimaryScreenView>[0];

function renderView(feed: ViewProps['feed'], extra: Partial<ViewProps> = {}) {
  return render(
    <PrimaryScreenView
      feed={feed}
      connectionState="connected"
      thinking={false}
      toasts={[]}
      onDismiss={noop}
      onAccept={noop}
      now={NOW}
      {...extra}
    />,
  );
}

describe('PrimaryScreenView', () => {
  beforeEach(() => {
    vi.mocked(acceptTicket).mockReset();
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('exposes the Feed region as the SSE-live gate with the connection state', () => {
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: updateCards }));
    const region = screen.getByRole('region', { name: 'Feed' });
    expect(region).toHaveAttribute('data-role', 'feed');
    expect(region).toHaveAttribute('data-connection-state', 'connected');
  });

  it('derives the header status: blockers → "N blocker(s) · M updates" (08 §2)', () => {
    renderView(
      makeFeedSnapshot({
        summary: { blocker_count: 1, update_count: 3, stream_count: 5 },
        cards: [blockerCard, ...updateCards],
      }),
    );
    expect(screen.getByText('1 blocker · 3 updates')).toHaveAttribute('data-role', 'feed-status');
  });

  it('derives the header status: no blockers → the active-stream count', () => {
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: updateCards }));
    expect(screen.getByText('5 streams')).toBeInTheDocument();
  });

  it('renders the blocker pinned first and the last-seen "Earlier" divider at the boundary (08 D2′, 4a)', () => {
    // last-seen at 20: update:30 is new (above), update:20/update:10 are history
    // (at/below) — the divider falls just before update:20.
    renderView(
      makeFeedSnapshot({
        summary: {
          blocker_count: 1,
          update_count: 1,
          stream_count: 5,
          last_seen_notification_id: 20,
        },
        cards: [blockerCard, ...updateCards],
      }),
      { lastSeenId: 20 },
    );
    const [firstCard] = screen.getAllByRole('article');
    if (firstCard === undefined) {
      throw new Error('expected at least one feed card');
    }
    expect(firstCard).toHaveAttribute('data-role', 'feed-card');
    expect(firstCard).toHaveAttribute('data-kind', 'blocker');
    expect(within(firstCard).getByText('Auth')).toHaveAttribute('data-role', 'feed-card-label');
    const divider = screen.getByText('Earlier');
    expect(divider).toHaveAttribute('data-role', 'feed-divider');
    // The divider sits immediately before the first history card (update:20).
    const slot = divider.closest('[data-role="backlog-slot"]');
    expect(slot?.querySelector('[data-role="feed-card-label"]')?.textContent).toBe('Email');
  });

  it('de-emphasizes already-seen cards below the boundary and leaves new cards above untouched (08 D2′)', () => {
    // last-seen at 20: update:30 is new (above), update:20/update:10 are history
    // (at/below) — the history cards recede via data-seen; the new card and the
    // blocker (no notification_id — still needs the user) do not.
    renderView(
      makeFeedSnapshot({
        summary: { blocker_count: 1, stream_count: 5, last_seen_notification_id: 20 },
        cards: [blockerCard, ...updateCards],
      }),
      { lastSeenId: 20 },
    );
    const seenState = (label: string): string | null => {
      const card = screen.getByText(label).closest('[data-role="feed-card"]');
      return card?.getAttribute('data-seen') ?? null;
    };
    // Above the divider (new) and the blocker stay bold/full — no data-seen.
    expect(seenState('Auth')).toBeNull();
    expect(seenState('Rate limiting')).toBeNull();
    // At/below the divider recede.
    expect(seenState('Email')).toBe('true');
    expect(seenState('Search')).toBe('true');
    // The tighter body clamp rides the same data-seen hook on the body element.
    const emailCard = screen.getByText('Email').closest('[data-role="feed-card"]');
    expect(emailCard?.querySelector('[data-role="feed-card-body"]')).toHaveAttribute(
      'data-seen',
      'true',
    );
  });

  it('keeps an already-seen card expandable in place from its tighter default (08 D2′)', () => {
    fakeClampedOverflow();
    // Everything shown is at/below the boundary → all history, all receded, but
    // the same expand/collapse interaction still applies.
    renderView(makeFeedSnapshot({ summary: { stream_count: 1 }, cards: updateCards }), {
      lastSeenId: 30,
    });
    const emailCard = screen.getByText('Email').closest('[data-role="feed-card"]');
    if (!(emailCard instanceof HTMLElement)) {
      throw new Error('expected the Email feed card');
    }
    expect(emailCard).toHaveAttribute('data-seen', 'true');
    const body = emailCard.querySelector('[data-role="feed-card-body"]');
    if (!(body instanceof HTMLElement)) {
      throw new Error('expected the Email feed card body');
    }
    // The clamped body is itself the toggle — no separate control.
    expect(body).toHaveAttribute('data-clickable', 'true');
    expect(body).not.toHaveAttribute('data-expanded');
    fireEvent.click(body);
    expect(body).toHaveAttribute('data-expanded', 'true');
  });

  it('does not de-emphasize any card without a last-seen boundary (08 D2′)', () => {
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: updateCards }));
    for (const card of screen.getAllByRole('article')) {
      expect(card).not.toHaveAttribute('data-seen');
    }
  });

  it('does not render the divider without a last-seen boundary (08 D2′)', () => {
    // No lastSeenId (fresh user / nothing seen) → all updates are current, no divider.
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: updateCards }));
    expect(screen.queryByText('Earlier')).toBeNull();
  });

  it('does not render the divider when every update is newer than the boundary (08 D2′)', () => {
    // last-seen below the oldest shown update → nothing is "history" yet.
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: updateCards }), {
      lastSeenId: 5,
    });
    expect(screen.queryByText('Earlier')).toBeNull();
  });

  it('shows the "Show earlier updates" affordance when history remains, wired to the loader (08 D2′)', () => {
    const onLoadMoreHistory = vi.fn();
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: updateCards }), {
      hasMoreHistory: true,
      onLoadMoreHistory,
    });
    const button = screen.getByRole('button', { name: 'Show earlier updates' });
    expect(button).toHaveAttribute('data-role', 'feed-load-more');
    button.click();
    expect(onLoadMoreHistory).toHaveBeenCalledTimes(1);
  });

  it('hides the "Show earlier updates" affordance when no history remains', () => {
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: updateCards }), {
      hasMoreHistory: false,
      onLoadMoreHistory: noop,
    });
    expect(screen.queryByRole('button', { name: /earlier updates/i })).toBeNull();
  });

  it('renders the preview image on a preview card (4c)', () => {
    const preview = makeFeedCard({
      kind: 'preview',
      id: 'update:40',
      label: 'Auth',
      body: "Here's the login screen running against the live endpoint.",
      notificationId: 40,
      imageUrl: 'https://cdn.example/login.png',
      createdAt: minutesAgo(0),
    });
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: [preview] }));
    const image = screen.getByRole('img');
    expect(image).toHaveAttribute('data-role', 'feed-card-image');
    expect(image).toHaveAttribute('src', 'https://cdn.example/login.png');
  });

  it('renders the all-clear empty state with only the building count and an active ember pulse (4d)', () => {
    renderView(
      makeFeedSnapshot({
        summary: {
          stream_count: 5,
          building: 3,
          idle: 2,
          last_word_at: minutesAgo(6),
        },
        cards: [],
      }),
    );
    expect(screen.getByText('All good.')).toHaveAttribute('data-role', 'feed-empty-title');
    // The idle count is dropped entirely — only the building count (with the
    // last-word suffix) is shown.
    expect(screen.getByText('3 building · last word 6m ago')).toBeInTheDocument();
    expect(screen.queryByText(/idle/)).not.toBeInTheDocument();
    // With active builds the pulse dot goes ember/animated via data-active.
    expect(document.querySelector('[data-role="feed-empty-pulse"]')).toHaveAttribute(
      'data-active',
      'true',
    );
    // No secondary/body copy under the headline (08 §4d): the status counts are
    // the focal content of the all-clear state.
    expect(document.querySelector('[data-role="feed-empty-body"]')).toBeNull();
    expect(screen.queryByText(/keeping your streams moving/)).not.toBeInTheDocument();
  });

  it('leaves the empty-state pulse dot flat/inactive when nothing is building', () => {
    renderView(
      makeFeedSnapshot({
        summary: {
          stream_count: 2,
          building: 0,
          idle: 2,
          last_word_at: minutesAgo(6),
        },
        cards: [],
      }),
    );
    expect(screen.getByText('0 building · last word 6m ago')).toBeInTheDocument();
    expect(document.querySelector('[data-role="feed-empty-pulse"]')).toHaveAttribute(
      'data-active',
      'false',
    );
  });

  it('renders a real Accept button on a proposal card and calls acceptTicket with the ticket id (08 §5)', () => {
    const proposal = makeFeedCard({
      kind: 'proposal',
      id: 'proposal:t-login',
      label: 'Login Redesign',
      body: 'Rework the login screen to a single-column layout with inline validation.',
      ticketId: 't-login',
      createdAt: minutesAgo(1),
    });
    render(
      <PrimaryScreenView
        feed={makeFeedSnapshot({ summary: { stream_count: 3 }, cards: [proposal] })}
        connectionState="connected"
        thinking={false}
        toasts={[]}
        onDismiss={noop}
        onAccept={(id) => {
          void acceptTicket(id);
        }}
        now={NOW}
      />,
    );
    const accept = screen.getByRole('button', { name: 'Accept' });
    expect(accept).toHaveAttribute('data-role', 'proposal-accept');
    fireEvent.click(accept);
    expect(acceptTicket).toHaveBeenCalledWith('t-login');
  });

  it('calls onAccept with the proposal ticket id when Accept is clicked', () => {
    const onAccept = vi.fn();
    const proposal = makeFeedCard({
      kind: 'proposal',
      id: 'proposal:t-x',
      label: 'X',
      body: 'body',
      ticketId: 't-x',
      createdAt: minutesAgo(1),
    });
    render(
      <PrimaryScreenView
        feed={makeFeedSnapshot({ cards: [proposal] })}
        connectionState="connected"
        thinking={false}
        toasts={[]}
        onDismiss={noop}
        onAccept={onAccept}
        now={NOW}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Accept' }));
    expect(onAccept).toHaveBeenCalledWith('t-x');
  });

  it('opens the full ticket detail overlay when a proposal card body is tapped (08 §5)', () => {
    const proposal = makeFeedCard({
      kind: 'proposal',
      id: 'proposal:t-login',
      label: 'Login Redesign',
      body: 'Rework the login screen to a single-column layout with inline validation.',
      ticketId: 't-login',
      createdAt: minutesAgo(1),
    });
    // The full ticket the card points at — its body carries the whole shaped
    // spec the feed digest clamps away, so the overlay shows more than the card.
    const ticket = makeTicket({
      id: 't-login',
      title: 'Login Redesign',
      body: `${'Rework the login screen to a single-column layout with inline validation. '.repeat(6)}Full acceptance criteria follow.`,
      state: 'shaping',
      priority: 2,
      createdAt: minutesAgo(5),
      updatedAt: minutesAgo(1),
    });
    const onAccept = vi.fn();
    render(
      <PrimaryScreenView
        feed={makeFeedSnapshot({ summary: { stream_count: 1 }, cards: [proposal] })}
        board={makeBoard({ shaping: [ticket] })}
        connectionState="connected"
        thinking={false}
        toasts={[]}
        onDismiss={noop}
        onAccept={onAccept}
        now={NOW}
      />,
    );
    // Nothing open until the card is tapped.
    expect(screen.queryByRole('dialog')).toBeNull();

    // The proposal body is a click-through button (not an inline paragraph),
    // with the quiet "Read full ticket" hint.
    const open = screen.getByRole('button', { name: 'Open ticket: Login Redesign' });
    expect(open).toHaveAttribute('data-role', 'feed-card-open');
    expect(screen.getByText('Read full ticket')).toBeInTheDocument();

    fireEvent.click(open);

    // The full ticket detail overlay opens, showing the full ticket body — the
    // whole record the feed digest elides, same surface every state gets.
    const dialog = screen.getByRole('dialog', { name: 'Ticket: Login Redesign' });
    // The body is rendered as Markdown, so the text sits in a child element (e.g.
    // a <p>) inside the ticket-detail-body container rather than on it directly.
    expect(
      within(dialog).getByText(ticket.body).closest('[data-role="ticket-detail-body"]'),
    ).not.toBeNull();

    // Accepting from the overlay flows the ticket id up and drains the overlay.
    fireEvent.click(within(dialog).getByRole('button', { name: 'Accept' }));
    expect(onAccept).toHaveBeenCalledWith('t-login');
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('leaves the body non-interactive (no toggle, no cue) when it fits within the clamp', () => {
    const update = makeFeedCard({
      kind: 'update',
      id: 'update:short',
      label: 'Short',
      body: 'A short body that fits.',
      notificationId: 5,
      createdAt: minutesAgo(1),
    });
    renderView(makeFeedSnapshot({ summary: { stream_count: 1 }, cards: [update] }));
    const body = document.querySelector('[data-role="feed-card-body"]');
    expect(body).not.toHaveAttribute('data-clickable');
    expect(body).not.toHaveAttribute('role', 'button');
    expect(screen.queryByRole('button', { name: /A short body/ })).toBeNull();
    // A body that fits carries no "tap to see more" cue.
    expect(body?.querySelector('[data-role="feed-card-more"]')).toBeNull();
  });

  it('reveals an overflowing card body in place on tap and collapses it again (uniform across kinds)', () => {
    fakeClampedOverflow();
    const update = makeFeedCard({
      kind: 'update',
      id: 'update:99',
      label: 'Verbose',
      body: 'A long update body that overflows the three-line clamp and must expand in place.',
      notificationId: 99,
      createdAt: minutesAgo(1),
    });
    renderView(makeFeedSnapshot({ summary: { stream_count: 1 }, cards: [update] }));

    // The clamped body is the toggle itself — tap it to expand, tap again to collapse.
    const body = document.querySelector('[data-role="feed-card-body"]');
    if (!(body instanceof HTMLElement)) {
      throw new Error('expected the update feed card body');
    }
    expect(body).toHaveAttribute('data-clickable', 'true');
    expect(body).toHaveAttribute('role', 'button');
    expect(body).toHaveAttribute('aria-expanded', 'false');
    expect(body).not.toHaveAttribute('data-expanded');

    fireEvent.click(body);
    expect(body).toHaveAttribute('data-expanded', 'true');
    expect(body).toHaveAttribute('aria-expanded', 'true');

    fireEvent.click(body);
    expect(body).not.toHaveAttribute('data-expanded');
    expect(body).toHaveAttribute('aria-expanded', 'false');
  });

  it('shows a "tap to see more" cue only while the body is clamped, as decoration (not a tap target)', () => {
    fakeClampedOverflow();
    const update = makeFeedCard({
      kind: 'update',
      id: 'update:cue',
      label: 'Verbose',
      body: 'A long update body that overflows the three-line clamp and must expand in place.',
      notificationId: 42,
      createdAt: minutesAgo(1),
    });
    renderView(makeFeedSnapshot({ summary: { stream_count: 1 }, cards: [update] }));

    const body = document.querySelector('[data-role="feed-card-body"]');
    if (!(body instanceof HTMLElement)) {
      throw new Error('expected the update feed card body');
    }
    // While clamped, the cue is present, labelled, and aria-hidden so it isn't a
    // separate control — the body button underneath owns the tap.
    const cue = body.querySelector('[data-role="feed-card-more"]');
    expect(cue).not.toBeNull();
    expect(cue).toHaveTextContent('tap to see more');
    expect(cue).toHaveAttribute('aria-hidden', 'true');
    expect(screen.queryByRole('button', { name: /tap to see more/i })).toBeNull();

    // Expanding drops the cue — the full body is visible, so it would be a lie.
    fireEvent.click(body);
    expect(body.querySelector('[data-role="feed-card-more"]')).toBeNull();
  });

  it('matches the DOM-structure snapshot: blocker + last-seen divider + load-more (4a, D2′)', () => {
    const { container } = renderView(
      makeFeedSnapshot({
        summary: {
          blocker_count: 1,
          update_count: 1,
          stream_count: 5,
          building: 3,
          idle: 2,
          last_seen_notification_id: 20,
        },
        cards: [blockerCard, ...updateCards],
        hasMoreHistory: true,
      }),
      { lastSeenId: 20, hasMoreHistory: true, onLoadMoreHistory: noop },
    );
    expect(container).toMatchSnapshot();
  });

  it('matches the DOM-structure snapshot: updates only (4b)', () => {
    const { container } = renderView(
      makeFeedSnapshot({ summary: { update_count: 3, stream_count: 5 }, cards: updateCards }),
    );
    expect(container).toMatchSnapshot();
  });

  it('matches the DOM-structure snapshot: preview (4c)', () => {
    const preview = makeFeedCard({
      kind: 'preview',
      id: 'update:40',
      label: 'Auth',
      body: "Here's the login screen running against the live endpoint.",
      notificationId: 40,
      imageUrl: 'https://cdn.example/login.png',
      createdAt: minutesAgo(0),
    });
    const { container } = renderView(
      makeFeedSnapshot({ summary: { stream_count: 5 }, cards: [preview, rateLimitUpdate] }),
    );
    expect(container).toMatchSnapshot();
  });

  it('matches the DOM-structure snapshot: all-clear (4d)', () => {
    const { container } = renderView(
      makeFeedSnapshot({
        summary: { stream_count: 5, building: 3, idle: 2, last_word_at: minutesAgo(6) },
        cards: [],
      }),
    );
    expect(container).toMatchSnapshot();
  });

  it('matches the DOM-structure snapshot: proposal card with Accept', () => {
    const proposal = makeFeedCard({
      kind: 'proposal',
      id: 'proposal:t-login',
      label: 'Login Redesign',
      body: 'Rework the login screen to a single-column layout with inline validation.',
      ticketId: 't-login',
      createdAt: minutesAgo(1),
    });
    const { container } = renderView(
      makeFeedSnapshot({ summary: { stream_count: 3 }, cards: [proposal] }),
    );
    expect(container).toMatchSnapshot();
  });
});
