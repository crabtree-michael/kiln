// Primary-screen view tests (08 §F image-snapshot targets + the Accept seam).
// DOM-structure snapshots stand in for pixel snapshots (same deferral as
// TicketCard/ChatPanel, 07 §9 D4). A fixed `now` is threaded through so the
// relative-age labels ("now", "2m", "1h") are deterministic across runs.
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { PrimaryScreenView } from '@/components/PrimaryScreenView';
import {
  makeBoard,
  makeFeedCard,
  makeFeedSnapshot,
  makeSystemAlert,
  makeTicket,
} from '@/test/fixtures';
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

// The dock is a live voice-store consumer (09), and the screen itself now reads
// `resume` off the voice store to hand a blocked ticket's Talk button off to the
// mic. These presentational tests render `PrimaryScreenView` directly (no
// `VoiceProvider`), so `useVoice` is mocked to a static resting state —
// deterministic, and no mic/socket I/O. `resume` is a hoisted spy shared across
// renders so the Talk hand-off can be asserted. The dock's own state rendering
// is covered by Dock.test.tsx / Dock.snapshot.test.tsx.
const { voiceResume } = vi.hoisted(() => ({ voiceResume: vi.fn() }));

vi.mock('@/voice/voice-context', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/voice/voice-context')>();
  return {
    ...actual,
    useVoice: () => ({
      micState: 'listening' as const,
      connecting: false,
      settledText: '',
      tailText: '',
      pause: vi.fn(),
      resume: voiceResume,
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
    voiceResume.mockReset();
  });
  afterEach(() => {
    vi.restoreAllMocks();
    // A deep-link test may have set ?ticket=; reset so it doesn't leak into the
    // next test (which would open an unexpected sheet on mount).
    window.history.replaceState(null, '', '/');
  });

  it('exposes the Feed region as the SSE-live gate with the connection state', () => {
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: updateCards }));
    const region = screen.getByRole('region', { name: 'Feed' });
    expect(region).toHaveAttribute('data-role', 'feed');
    expect(region).toHaveAttribute('data-connection-state', 'connected');
  });

  it('derives the header status: the ticket count, never blocker text, even with a blocker present (08 §2)', () => {
    renderView(
      makeFeedSnapshot({
        summary: { blocker_count: 1, update_count: 3, stream_count: 5 },
        cards: [blockerCard, ...updateCards],
      }),
    );
    expect(screen.getByText('5 tickets')).toHaveAttribute('data-role', 'feed-status');
  });

  it('derives the header status: no blockers → the active-ticket count', () => {
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: updateCards }));
    expect(screen.getByText('5 tickets')).toBeInTheDocument();
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

  it('renders a poke card as the ticket title with a 👉 and no body', () => {
    const poke = makeFeedCard({
      kind: 'poke',
      id: 'update:51',
      label: 'Auth',
      body: '',
      notificationId: 51,
      createdAt: minutesAgo(0),
    });
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: [poke] }));
    const card = screen.getByText('Auth').closest('[data-role="feed-card"]');
    expect(card).toHaveAttribute('data-kind', 'poke');
    expect(card?.querySelector('[data-role="feed-card-poke"]')?.textContent).toContain('👉');
    // A poke carries no body — the emoji is the whole signal.
    expect(card?.querySelector('[data-role="feed-card-body"]')).toBeNull();
  });

  it('renders a done card as the ticket title with a ✅ and no body', () => {
    const done = makeFeedCard({
      kind: 'done',
      id: 'update:73',
      label: 'Auth',
      body: '',
      notificationId: 73,
      createdAt: minutesAgo(0),
    });
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: [done] }));
    const card = screen.getByText('Auth').closest('[data-role="feed-card"]');
    expect(card).toHaveAttribute('data-kind', 'done');
    expect(card?.querySelector('[data-role="feed-card-done"]')?.textContent).toContain('✅');
    // Like a poke, a done card carries no body — the ✅ + title is the whole card.
    expect(card?.querySelector('[data-role="feed-card-body"]')).toBeNull();
  });

  it('wraps a done card in the swipe-to-clear affordance when a dismiss handler is wired (08 §3)', () => {
    const done = makeFeedCard({
      kind: 'done',
      id: 'update:73',
      label: 'Auth',
      body: '',
      notificationId: 73,
      createdAt: minutesAgo(0),
    });
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: [done] }), {
      onDismissCard: noop,
    });
    const card = screen.getByText('Auth').closest('[data-role="feed-card"]');
    // Like update/preview, a done card is a stray notification the user can wave
    // off — it rides inside the swipe-content the gesture translates.
    expect(card?.closest('[data-role="swipe-content"]')).not.toBeNull();
  });

  it('wraps a poke card in the swipe-to-clear affordance when a dismiss handler is wired (08 §3)', () => {
    const poke = makeFeedCard({
      kind: 'poke',
      id: 'update:51',
      label: 'Auth',
      body: '',
      notificationId: 51,
      createdAt: minutesAgo(0),
    });
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: [poke] }), {
      onDismissCard: noop,
    });
    const card = screen.getByText('Auth').closest('[data-role="feed-card"]');
    // Like update/preview/done, a poke is a stray notification the user can wave
    // off — only a blocker (which needs an explicit decision) stays static.
    expect(card?.closest('[data-role="swipe-content"]')).not.toBeNull();
  });

  it('leaves a blocker card static even when a dismiss handler is wired — it needs an explicit decision (08 §3)', () => {
    renderView(
      makeFeedSnapshot({
        summary: { blocker_count: 1, stream_count: 5 },
        cards: [blockerCard],
      }),
      { onDismissCard: noop },
    );
    const card = screen.getByText(blockerCard.label).closest('[data-role="feed-card"]');
    expect(card).toHaveAttribute('data-kind', 'blocker');
    // A blocker is board state that demands a user decision — never swipe-clearable.
    expect(card?.closest('[data-role="swipe-content"]')).toBeNull();
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

    // The proposal body is a click-through button (not an inline paragraph). Its
    // only cue is the shared clamped-body "tap to see more" every kind wears; the
    // old left-aligned "Read full ticket" hint is gone.
    const open = screen.getByRole('button', { name: 'Open ticket: Login Redesign' });
    expect(open).toHaveAttribute('data-role', 'feed-card-open');
    expect(screen.queryByText('Read full ticket')).toBeNull();

    fireEvent.click(open);

    // The full ticket detail sheet opens, showing the full ticket body — the
    // whole record the feed digest elides, same surface every state gets. The
    // dialog is named by its visible title (Radix aria-labelledby).
    const dialog = screen.getByRole('dialog', { name: 'Login Redesign' });
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

  it('opens the linked ticket detail when a ticket-linked update card is tapped', () => {
    // An activity update carrying a ticket_id is a shortcut into that ticket's
    // context: tapping the body opens the same detail overlay as a proposal.
    const update = makeFeedCard({
      kind: 'update',
      id: 'update:77',
      label: 'Auth',
      body: 'Added a fixed-window limiter on /auth — 20 requests a minute per IP. Suite is green.',
      ticketId: 't-auth',
      notificationId: 77,
      createdAt: minutesAgo(2),
    });
    const ticket = makeTicket({
      id: 't-auth',
      title: 'Auth',
      body: `${'Rate limiting and the auth handshake, spelled out in full. '.repeat(6)}Acceptance criteria follow.`,
      state: 'working',
      priority: 1,
      createdAt: minutesAgo(30),
      updatedAt: minutesAgo(2),
    });
    render(
      <PrimaryScreenView
        feed={makeFeedSnapshot({ summary: { stream_count: 1 }, cards: [update] })}
        board={makeBoard({ working: [ticket] })}
        connectionState="connected"
        thinking={false}
        toasts={[]}
        onDismiss={noop}
        onAccept={noop}
        now={NOW}
      />,
    );
    // Nothing open until the card is tapped.
    expect(screen.queryByRole('dialog')).toBeNull();

    // The update body is a click-through button (`feed-card-open`), not an
    // expand-in-place paragraph — the same affordance a proposal card wears.
    const open = screen.getByRole('button', { name: 'Open ticket: Auth' });
    expect(open).toHaveAttribute('data-role', 'feed-card-open');

    fireEvent.click(open);

    // The full ticket detail sheet opens, showing the whole ticket body the feed
    // digest clamps away. The dialog is named by its visible title.
    const dialog = screen.getByRole('dialog', { name: 'Auth' });
    expect(
      within(dialog).getByText(ticket.body).closest('[data-role="ticket-detail-body"]'),
    ).not.toBeNull();
  });

  it('keeps a ticket-less update card expandable in place (no click-through)', () => {
    // An authored note with no linked ticket has nowhere to open — it stays the
    // expand-in-place body, never the click-through button, even with a board.
    fakeClampedOverflow();
    const update = makeFeedCard({
      kind: 'update',
      id: 'update:88',
      label: 'General',
      body: 'A standalone status note the brain posted with no ticket behind it, long enough to clamp.',
      notificationId: 88,
      createdAt: minutesAgo(2),
    });
    renderView(makeFeedSnapshot({ summary: { stream_count: 1 }, cards: [update] }), {
      board: makeBoard({}),
    });
    // No click-through affordance; the body itself is the in-place toggle.
    expect(screen.queryByRole('button', { name: /Open ticket/ })).toBeNull();
    const body = document.querySelector('[data-role="feed-card-body"]');
    if (!(body instanceof HTMLElement)) {
      throw new Error('expected the update feed card body');
    }
    expect(body).toHaveAttribute('data-clickable', 'true');
    fireEvent.click(body);
    expect(body).toHaveAttribute('data-expanded', 'true');
  });

  it('opens the ticket detail from a done card head (a body-less card links from its head)', () => {
    // A done card is just ✅ + ticket title, no body — so its head is the tap
    // target that opens the completed ticket's detail (08 §7 → §5).
    const done = makeFeedCard({
      kind: 'done',
      id: 'update:73',
      label: 'Auth',
      body: '',
      ticketId: 't-auth',
      notificationId: 73,
      createdAt: minutesAgo(0),
    });
    const ticket = makeTicket({
      id: 't-auth',
      title: 'Auth',
      body: 'The whole shipped auth flow, the record the done card points at.',
      state: 'done',
      priority: 1,
      createdAt: minutesAgo(120),
      updatedAt: minutesAgo(10),
    });
    renderView(makeFeedSnapshot({ summary: { stream_count: 1 }, cards: [done] }), {
      board: makeBoard({ done: [ticket] }),
    });
    // Nothing open until the card is tapped.
    expect(screen.queryByRole('dialog')).toBeNull();
    // The head itself is the tap target — a button reset to read like the plain head.
    const open = screen.getByRole('button', { name: 'Open ticket: Auth' });
    expect(open).toHaveAttribute('data-role', 'feed-card-head');

    fireEvent.click(open);

    const dialog = screen.getByRole('dialog', { name: 'Auth' });
    expect(
      within(dialog).getByText('Done').closest('[data-role="ticket-detail-status"]'),
    ).toHaveAttribute('data-state', 'done');
  });

  it('opens the ticket detail from a poke card head', () => {
    // A poke is the steward's body-less stall nudge (👉 + title); its head links
    // into the stalled ticket so tapping the nudge jumps straight to the work.
    const poke = makeFeedCard({
      kind: 'poke',
      id: 'update:51',
      label: 'Auth',
      body: '',
      ticketId: 't-auth',
      notificationId: 51,
      createdAt: minutesAgo(0),
    });
    const ticket = makeTicket({
      id: 't-auth',
      title: 'Auth',
      body: 'The stalled auth work the poke nudges.',
      state: 'working',
      priority: 1,
      createdAt: minutesAgo(30),
      updatedAt: minutesAgo(5),
    });
    renderView(makeFeedSnapshot({ summary: { stream_count: 1 }, cards: [poke] }), {
      board: makeBoard({ working: [ticket] }),
    });
    fireEvent.click(screen.getByRole('button', { name: 'Open ticket: Auth' }));
    expect(screen.getByRole('dialog', { name: 'Auth' })).toBeInTheDocument();
  });

  it('leaves a body-less card head static when it carries no ticket link', () => {
    // A done/poke card with no ticket_id has nowhere to open — the head stays a
    // plain row, never a button (mirrors a ticket-less update staying expandable).
    const done = makeFeedCard({
      kind: 'done',
      id: 'update:74',
      label: 'Auth',
      body: '',
      notificationId: 74,
      createdAt: minutesAgo(0),
    });
    renderView(makeFeedSnapshot({ summary: { stream_count: 1 }, cards: [done] }), {
      board: makeBoard({}),
    });
    expect(screen.queryByRole('button', { name: /Open ticket/ })).toBeNull();
    const head = document.querySelector('[data-role="feed-card-head"]');
    expect(head?.tagName).toBe('DIV');
  });

  describe('ticket detail affordances by state (deep-linked open)', () => {
    // A push-notification tap deep-links a ticket open by id (02 §10). Unlike a
    // proposal click-through (always Shaping), this opens whatever state the
    // ticket is now in — including the blocked/done tickets a notification points
    // at. Simulate the cold-open path by seeding the ?ticket= query before render.
    const openDeepLink = (id: string): void => {
      window.history.replaceState(null, '', `/?ticket=${id}`);
    };

    it('opens a blocked ticket with a Talk button (no Accept) and hands off to the mic on tap', () => {
      const blocked = makeTicket({
        id: 't-stuck',
        title: 'Stuck ticket',
        body: 'body',
        state: 'blocked',
        priority: 1,
        createdAt: minutesAgo(30),
        updatedAt: minutesAgo(5),
        blockedReason: 'Needs a decision on the auth scheme.',
      });
      openDeepLink('t-stuck');
      renderView(makeFeedSnapshot({ summary: { stream_count: 1 }, cards: [] }), {
        board: makeBoard({ blocked: [blocked] }),
      });

      const dialog = screen.getByRole('dialog', { name: 'Stuck ticket' });
      // A blocked ticket is discussed, not accepted.
      expect(within(dialog).queryByRole('button', { name: 'Accept' })).toBeNull();
      const talk = within(dialog).getByRole('button', { name: 'Talk to unblock' });

      fireEvent.click(talk);
      // Talk closes the sheet (uncovering the dock) and turns the mic on.
      expect(voiceResume).toHaveBeenCalledTimes(1);
      expect(screen.queryByRole('dialog')).toBeNull();
    });

    it('opens a done ticket with a "done" status indicator and no Accept', () => {
      const done = makeTicket({
        id: 't-shipped',
        title: 'Shipped ticket',
        body: 'body',
        state: 'done',
        priority: 1,
        createdAt: minutesAgo(120),
        updatedAt: minutesAgo(10),
      });
      openDeepLink('t-shipped');
      renderView(makeFeedSnapshot({ summary: { stream_count: 1 }, cards: [] }), {
        board: makeBoard({ done: [done] }),
      });

      const dialog = screen.getByRole('dialog', { name: 'Shipped ticket' });
      const status = within(dialog).getByText('Done').closest('[data-role="ticket-detail-status"]');
      expect(status).toHaveAttribute('data-state', 'done');
      // Completed work has nothing to accept, and no Talk either.
      expect(within(dialog).queryByRole('button', { name: 'Accept' })).toBeNull();
      expect(within(dialog).queryByRole('button', { name: 'Talk to unblock' })).toBeNull();
    });
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

  it('shows the permanent error band in the dock region when the board carries alerts', () => {
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: [] }), {
      board: makeBoard({ alerts: [makeSystemAlert('2 of 5 sandboxes failing')] }),
    });
    const band = screen.getByRole('alert');
    expect(band).toHaveTextContent('2 of 5 sandboxes failing');
    expect(band.closest('[data-role="dock-region"]')).not.toBeNull();
  });

  it('renders no error band when the board has no alerts', () => {
    renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: [] }), {
      board: makeBoard(),
    });
    expect(screen.queryByRole('alert')).toBeNull();
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

  describe('clear-all trash affordance (08 §3)', () => {
    const proposalCard = makeFeedCard({
      kind: 'proposal',
      id: 'proposal:t-login',
      label: 'Login',
      body: 'Rework the login screen.',
      ticketId: 't-login',
      createdAt: minutesAgo(1),
    });

    it('is absent unless a clear-all handler is wired (presentational default)', () => {
      renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: updateCards }));
      expect(screen.queryByRole('button', { name: 'Clear all notifications' })).toBeNull();
    });

    it('clears every notification only after the confirm is accepted', () => {
      const onDismissAll = vi.fn();
      const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
      renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: updateCards }), {
        onDismissAll,
      });
      fireEvent.click(screen.getByRole('button', { name: 'Clear all notifications' }));
      expect(confirm).toHaveBeenCalledWith('Clear all notifications?');
      expect(onDismissAll).toHaveBeenCalledTimes(1);
    });

    it('leaves the feed untouched when the confirm is cancelled', () => {
      const onDismissAll = vi.fn();
      const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
      renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: updateCards }), {
        onDismissAll,
      });
      fireEvent.click(screen.getByRole('button', { name: 'Clear all notifications' }));
      expect(confirm).toHaveBeenCalledWith('Clear all notifications?');
      expect(onDismissAll).not.toHaveBeenCalled();
    });

    it('is disabled when only board cards (or none) are clearable', () => {
      renderView(makeFeedSnapshot({ summary: { stream_count: 5 }, cards: [proposalCard] }), {
        onDismissAll: noop,
      });
      expect(screen.getByRole('button', { name: 'Clear all notifications' })).toBeDisabled();
    });

    it('is enabled when a notification-backed card is present', () => {
      renderView(
        makeFeedSnapshot({ summary: { stream_count: 5 }, cards: [proposalCard, rateLimitUpdate] }),
        { onDismissAll: noop },
      );
      expect(screen.getByRole('button', { name: 'Clear all notifications' })).toBeEnabled();
    });
  });
});
