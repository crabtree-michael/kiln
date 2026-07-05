// Primary-screen view tests (08 §F image-snapshot targets + the Accept seam).
// DOM-structure snapshots stand in for pixel snapshots (same deferral as
// TicketCard/ChatPanel, 07 §9 D4). A fixed `now` is threaded through so the
// relative-age labels ("now", "2m", "1h") are deterministic across runs.
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { PrimaryScreenView } from '@/components/PrimaryScreenView';
import { makeBoard, makeFeedCard, makeFeedSnapshot, makeTicket } from '@/test/fixtures';
import { acceptTicket } from '@/transport/transport';

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

function renderView(feed: Parameters<typeof PrimaryScreenView>[0]['feed']) {
  return render(
    <PrimaryScreenView
      feed={feed}
      connectionState="connected"
      thinking={false}
      toasts={[]}
      onDismiss={noop}
      onAccept={noop}
      now={NOW}
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

  it('renders the blocker card pinned first with its kind and the "While you were away" divider (4a)', () => {
    renderView(
      makeFeedSnapshot({
        summary: { blocker_count: 1, update_count: 3, stream_count: 5 },
        cards: [blockerCard, ...updateCards],
      }),
    );
    const [firstCard] = screen.getAllByRole('article');
    if (firstCard === undefined) {
      throw new Error('expected at least one feed card');
    }
    expect(firstCard).toHaveAttribute('data-role', 'feed-card');
    expect(firstCard).toHaveAttribute('data-kind', 'blocker');
    expect(within(firstCard).getByText('Auth')).toHaveAttribute('data-role', 'feed-card-label');
    expect(screen.getByText('While you were away')).toHaveAttribute('data-role', 'feed-divider');
  });

  it('does not render the divider when updates are not preceded by a blocker/proposal (4b)', () => {
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: updateCards }));
    expect(screen.queryByText('While you were away')).toBeNull();
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
    expect(screen.getByText('Nothing needs you right now.')).toHaveAttribute(
      'data-role',
      'feed-empty-title',
    );
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

  it('renders a compact summary on the proposal card, not the full body (08 §5)', () => {
    const longBody = `${'A'.repeat(300)} tail`;
    const proposal = makeFeedCard({
      kind: 'proposal',
      id: 'proposal:t-long',
      label: 'Long Proposal',
      body: longBody,
      ticketId: 't-long',
      createdAt: minutesAgo(1),
    });
    renderView(makeFeedSnapshot({ summary: { stream_count: 1 }, cards: [proposal] }));
    const body = document.querySelector('[data-role="feed-card-body"]');
    expect(body?.textContent ?? '').toMatch(/…$/);
    expect((body?.textContent ?? '').length).toBeLessThan(longBody.length);
    // The full body is behind the click-through, never dumped into the feed.
    expect(screen.queryByText(longBody)).toBeNull();
  });

  it('opens the ticket detail overlay with the full ticket when a proposal card is clicked', () => {
    const body = 'The complete shaped body the overlay shows in full, not the feed.';
    const proposal = makeFeedCard({
      kind: 'proposal',
      id: 'proposal:t-x',
      label: 'Detail Me',
      body,
      ticketId: 't-x',
      createdAt: minutesAgo(1),
    });
    const ticket = makeTicket({
      id: 't-x',
      title: 'Detail Me',
      body,
      state: 'shaping',
      priority: 2,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-02T00:00:00Z',
    });
    render(
      <PrimaryScreenView
        feed={makeFeedSnapshot({ cards: [proposal] })}
        board={makeBoard({ shaping: [ticket] })}
        connectionState="connected"
        thinking={false}
        toasts={[]}
        onDismiss={noop}
        onAccept={noop}
        now={NOW}
      />,
    );
    expect(screen.queryByRole('dialog')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Open ticket: Detail Me' }));
    const dialog = screen.getByRole('dialog', { name: 'Ticket: Detail Me' });
    expect(within(dialog).getByText(body)).toBeInTheDocument();
  });

  it('accepts from the detail overlay with the ticket id and then dismisses it', () => {
    const onAccept = vi.fn();
    const body = 'Shaped body for the accept-from-detail path.';
    const proposal = makeFeedCard({
      kind: 'proposal',
      id: 'proposal:t-y',
      label: 'Accept Me',
      body,
      ticketId: 't-y',
      createdAt: minutesAgo(1),
    });
    const ticket = makeTicket({
      id: 't-y',
      title: 'Accept Me',
      body,
      state: 'shaping',
      priority: 1,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-02T00:00:00Z',
    });
    render(
      <PrimaryScreenView
        feed={makeFeedSnapshot({ cards: [proposal] })}
        board={makeBoard({ shaping: [ticket] })}
        connectionState="connected"
        thinking={false}
        toasts={[]}
        onDismiss={noop}
        onAccept={onAccept}
        now={NOW}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Open ticket: Accept Me' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Accept' }));
    expect(onAccept).toHaveBeenCalledWith('t-y');
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('tapping inline Accept does not open the detail overlay', () => {
    const body = 'Inline accept must accept without navigating to the detail.';
    const proposal = makeFeedCard({
      kind: 'proposal',
      id: 'proposal:t-z',
      label: 'Inline',
      body,
      ticketId: 't-z',
      createdAt: minutesAgo(1),
    });
    const ticket = makeTicket({
      id: 't-z',
      title: 'Inline',
      body,
      state: 'shaping',
      priority: 1,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-02T00:00:00Z',
    });
    render(
      <PrimaryScreenView
        feed={makeFeedSnapshot({ cards: [proposal] })}
        board={makeBoard({ shaping: [ticket] })}
        connectionState="connected"
        thinking={false}
        toasts={[]}
        onDismiss={noop}
        onAccept={noop}
        now={NOW}
      />,
    );
    // Only the inline Accept exists while the overlay is closed → unambiguous.
    fireEvent.click(screen.getByRole('button', { name: 'Accept' }));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('matches the DOM-structure snapshot: blocker + while-you-were-away (4a)', () => {
    const { container } = renderView(
      makeFeedSnapshot({
        summary: { blocker_count: 1, update_count: 3, stream_count: 5, building: 3, idle: 2 },
        cards: [blockerCard, ...updateCards],
      }),
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
