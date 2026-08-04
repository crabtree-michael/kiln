// The desktop shell (13 §3–§10). Presentational, so it renders straight from
// fixtures with no stores behind it. The cases below are the document's claims
// made checkable: two regions and only two, the feed scoped to the selected
// project, the states it has to hold, detail over the feed rather than beside
// it, and a keyboard path through the column.
import { describe, it, expect, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { DesktopScreenView } from '@/components/desktop/DesktopScreenView';
import type { RailProject } from '@/components/desktop/ProjectsRail';
import {
  makeAgentStatus,
  makeBoard,
  makeFeedCard,
  makeFeedSnapshot,
  makeTicket,
} from '@/test/fixtures';

// The in-sheet mic and its transcript are live voice-store consumers (09). These
// presentational tests render the shell directly (no `VoiceProvider`), so
// `useVoice` is mocked to a static resting state — deterministic, and no
// mic/socket I/O. Same stance as PrimaryScreenView.test.tsx.
vi.mock('@/voice/voice-context', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/voice/voice-context')>();
  return {
    ...actual,
    useVoice: () => ({
      micState: 'paused' as const,
      connecting: false,
      settledText: '',
      tailText: '',
      pause: vi.fn(),
      resume: vi.fn(),
      cancel: vi.fn(),
      sendNow: vi.fn(),
      countingDown: false,
      sendImminent: false,
      delaySend: vi.fn(),
      getSendCountdown: vi.fn(() => null),
      getLevel: vi.fn(() => 0),
      keyboardMode: false,
      openKeyboard: vi.fn(),
      closeKeyboard: vi.fn(),
      submitText: vi.fn(() => Promise.resolve(true)),
      setTicketContext: vi.fn(),
    }),
  };
});

const NOW = Date.parse('2026-08-04T12:00:00Z');

const PROJECTS: RailProject[] = [
  { id: 'p1', name: 'kiln', state: 'quiet', working: 0 },
  { id: 'p2', name: 'atlas', state: 'needs-you', working: 0 },
];

/** A ticket an agent is mid-turn on, for the working-strip cases. */
function workingTicket(id: string, title: string, statusChangedAt: string) {
  return makeTicket({
    id,
    title,
    body: 'the full record',
    state: 'working',
    priority: 1,
    createdAt: '2026-08-04T09:00:00Z',
    updatedAt: statusChangedAt,
    statusChangedAt,
  });
}

const blockerCard = makeFeedCard({
  kind: 'blocker',
  id: 'c1',
  label: 'auth refresh',
  body: 'The refresh endpoint returns 401 for rotated tokens. Do you want me to retry once with a fresh token, or surface it to the user?',
  createdAt: '2026-08-04T11:30:00Z',
  ticketId: 't1',
});

const updateCard = makeFeedCard({
  kind: 'update',
  id: 'c2',
  label: 'poller',
  body: 'Landed the retry.',
  createdAt: '2026-08-04T11:00:00Z',
  notificationId: 12,
});

/** The feed region owns the arrow-key handler, so those cases fire at it
 * directly. Narrows without a type assertion (the lint gate bans both `as` and
 * `!`) by throwing if it is missing. */
function feedRegion(container: HTMLElement): HTMLElement {
  const node = container.querySelector<HTMLElement>('[data-role="desktop-feed"]');
  if (node === null) {
    throw new Error('desktop-feed not found');
  }
  return node;
}

function renderShell(overrides: Partial<React.ComponentProps<typeof DesktopScreenView>> = {}) {
  const onSelectProject = vi.fn();
  const onAccept = vi.fn();
  const result = render(
    <DesktopScreenView
      projects={PROJECTS}
      currentProjectId="p1"
      onSelectProject={onSelectProject}
      feed={makeFeedSnapshot({ cards: [blockerCard, updateCard] })}
      board={makeBoard()}
      connectionState="connected"
      thinking={false}
      toasts={[]}
      onDismiss={vi.fn()}
      onAccept={onAccept}
      now={NOW}
      {...overrides}
    />,
  );
  return { ...result, onSelectProject, onAccept };
}

describe('DesktopScreenView', () => {
  it('is two regions and only two — a rail and a feed, no third pane, no board', () => {
    const { container } = renderShell();
    expect(container.querySelector('[data-role="desktop-rail"]')).not.toBeNull();
    expect(container.querySelector('[data-role="desktop-feed"]')).not.toBeNull();
    // The Kanban board is not revived on desktop (13 D2).
    expect(container.querySelector('[data-role="board"]')).toBeNull();
    expect(container.querySelector('[data-role="board-column"]')).toBeNull();
    // And there is no inspector/detail pane beside the feed (13 D7).
    expect(container.querySelector('[data-role="desktop-inspector"]')).toBeNull();
  });

  it('renders the rail from the projects it is given and switches on click', () => {
    const { onSelectProject } = renderShell();
    fireEvent.click(screen.getByRole('button', { name: /atlas/ }));
    expect(onSelectProject).toHaveBeenCalledWith('p2');
  });

  it("shows the selected project's cards, newest first, in one column", () => {
    const { container } = renderShell();
    const labels = Array.from(
      container.querySelectorAll<HTMLElement>('[data-role="feed-card-label"]'),
    ).map((node) => node.textContent);
    expect(labels).toEqual(['auth refresh', 'poller']);
  });

  it('needs-you: the blocker question is on screen in full, not opened first', () => {
    renderShell();
    expect(screen.getByText(/retry once with a fresh token/)).toBeInTheDocument();
  });

  it('resting: composed and honest, not an apology', () => {
    const { container } = renderShell({ feed: makeFeedSnapshot({ cards: [] }) });
    expect(container.querySelector('[data-role="desktop-rest"]')).not.toBeNull();
    expect(screen.getByText('All quiet.')).toBeInTheDocument();
  });

  it('working: the breathing indication is on while the brain is mid-pass', () => {
    const { container } = renderShell({ thinking: true });
    expect(container.querySelector('[data-role="desktop-working"]')).not.toBeNull();
  });

  it('working: also on while agents are mid-turn, with the feed otherwise still', () => {
    const { container } = renderShell({
      feed: makeFeedSnapshot({ cards: [updateCard], summary: { building: 2 } }),
    });
    expect(container.querySelector('[data-role="desktop-working"]')).not.toBeNull();
  });

  it('working: NAMES the tickets being worked, so "what is in progress" is answered at a glance', () => {
    const { container } = renderShell({
      board: makeBoard({
        working: [
          workingTicket('t1', 'auth refresh', '2026-08-04T11:00:00Z'),
          workingTicket('t2', 'poller', '2026-08-04T11:50:00Z'),
        ],
      }),
      feed: makeFeedSnapshot({ cards: [updateCard], summary: { building: 2 } }),
    });
    const titles = Array.from(
      container.querySelectorAll<HTMLElement>('[data-role="desktop-working-title"]'),
    ).map((node) => node.textContent);
    // Oldest-started first: a ticket picked up now appends, it does not shove
    // the rows above it down.
    expect(titles).toEqual(['auth refresh', 'poller']);
  });

  it('working: the strip sits ABOVE the feed scroll region, so it cannot be scrolled away', () => {
    const { container } = renderShell({
      board: makeBoard({ working: [workingTicket('t1', 'auth refresh', '2026-08-04T11:00:00Z')] }),
      thinking: true,
    });
    const strip = container.querySelector('[data-role="desktop-working"]');
    expect(strip).not.toBeNull();
    // In flow in the main column, and NOT inside the feed's own scroller.
    expect(
      container.querySelector('[data-role="desktop-feed"] [data-role="desktop-working"]'),
    ).toBeNull();
    expect(strip?.parentElement).toHaveAttribute('data-role', 'desktop-main');
  });

  it('working: shows how long each ticket has been at it, and says so in words for AT', () => {
    renderShell({
      board: makeBoard({ working: [workingTicket('t1', 'auth refresh', '2026-08-04T11:00:00Z')] }),
      thinking: true,
    });
    expect(
      screen.getByRole('button', { name: 'Open working ticket: auth refresh — working for 1h' }),
    ).toBeInTheDocument();
  });

  it('working: a dead session behind a Working ticket is stated, not painted over', () => {
    // The one lie the strip must not tell: a ticket parked in Working whose
    // sandbox has failed is not "working".
    renderShell({
      board: makeBoard({
        working: [workingTicket('t1', 'auth refresh', '2026-08-04T11:00:00Z')],
        agents: [makeAgentStatus('t1', 'errored')],
      }),
      thinking: true,
    });
    expect(screen.getByText('failing')).toBeInTheDocument();
  });

  it('working: opening a strip row opens that ticket over the feed', () => {
    const { container } = renderShell({
      board: makeBoard({ working: [workingTicket('t1', 'auth refresh', '2026-08-04T11:00:00Z')] }),
      thinking: true,
    });
    fireEvent.click(
      screen.getByRole('button', { name: 'Open working ticket: auth refresh — working for 1h' }),
    );
    expect(screen.getByRole('dialog', { name: 'auth refresh' })).toBeInTheDocument();
    expect(container.querySelector('[data-role="desktop-feed"]')).not.toBeNull();
  });

  it('working: the brain thinking with nothing in Working still shows the indication, bare', () => {
    const { container } = renderShell({ thinking: true });
    expect(container.querySelector('[data-role="desktop-working-head"]')).not.toBeNull();
    expect(container.querySelector('[data-role="desktop-working-list"]')).toBeNull();
  });

  it('working: a board naming a working ticket lights the strip even before the summary agrees', () => {
    const { container } = renderShell({
      board: makeBoard({ working: [workingTicket('t1', 'auth refresh', '2026-08-04T11:00:00Z')] }),
    });
    expect(container.querySelector('[data-role="desktop-working"]')).not.toBeNull();
  });

  it('working: no counts and no meters — the strip lists, it does not measure (13 §8)', () => {
    const { container } = renderShell({
      board: makeBoard({
        working: [
          workingTicket('t1', 'auth refresh', '2026-08-04T11:00:00Z'),
          workingTicket('t2', 'poller', '2026-08-04T11:50:00Z'),
        ],
      }),
      thinking: true,
    });
    const strip = container.querySelector('[data-role="desktop-working"]');
    expect(strip?.querySelector('progress')).toBeNull();
    expect(strip?.textContent).not.toMatch(/2 (working|tickets)/);
  });

  it('resting: no working indication when nothing is in motion', () => {
    const { container } = renderShell();
    expect(container.querySelector('[data-role="desktop-working"]')).toBeNull();
  });

  it('disconnected: stated permanently and in place — never a modal', () => {
    const { container } = renderShell({ connectionState: 'reconnecting' });
    const band = container.querySelector('[data-role="desktop-connection"]');
    expect(band).not.toBeNull();
    expect(band?.textContent).toMatch(/not receiving updates/);
    // In flow in the rail, not a dialog over the app.
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(container.querySelector('[data-role="desktop-feed"]')).not.toBeNull();
  });

  it('connected: no connection band at all', () => {
    const { container } = renderShell();
    expect(container.querySelector('[data-role="desktop-connection"]')).toBeNull();
  });

  it('opens ticket detail OVER the feed, and the feed stays mounted behind it', () => {
    const ticket = makeTicket({
      id: 't1',
      title: 'auth refresh',
      body: 'the full record',
      state: 'shaping',
      priority: 1,
      createdAt: '2026-08-04T10:00:00Z',
      updatedAt: '2026-08-04T11:00:00Z',
    });
    const proposal = makeFeedCard({
      kind: 'proposal',
      id: 'c3',
      label: 'auth refresh',
      body: 'a proposal body',
      createdAt: '2026-08-04T11:30:00Z',
      ticketId: 't1',
    });
    const { container } = renderShell({
      feed: makeFeedSnapshot({ cards: [proposal] }),
      board: makeBoard({ shaping: [ticket] }),
    });

    fireEvent.click(screen.getByRole('button', { name: 'Open ticket: auth refresh' }));
    // The sheet portals to document.body, so query it via `screen`, not `container`.
    expect(screen.getByRole('dialog', { name: 'auth refresh' })).toBeInTheDocument();
    expect(container.querySelector('[data-role="desktop-feed"]')).not.toBeNull();
  });

  it('accepts a proposal in place, without opening it first', () => {
    const proposal = makeFeedCard({
      kind: 'proposal',
      id: 'c3',
      label: 'auth refresh',
      body: 'a proposal body',
      createdAt: '2026-08-04T11:30:00Z',
      ticketId: 't1',
    });
    const { onAccept } = renderShell({ feed: makeFeedSnapshot({ cards: [proposal] }) });
    fireEvent.click(screen.getByRole('button', { name: 'Accept' }));
    expect(onAccept).toHaveBeenCalledWith('t1');
  });

  it('arrow keys move between cards in the feed', () => {
    const { container } = renderShell();
    const feed = feedRegion(container);
    const feedRows = Array.from(
      container.querySelectorAll<HTMLElement>('[data-role="desktop-feed-row"]'),
    );
    expect(feedRows).toHaveLength(2);

    fireEvent.keyDown(feed, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(feedRows[0]);
    fireEvent.keyDown(feed, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(feedRows[1]);
    fireEvent.keyDown(feed, { key: 'ArrowUp' });
    expect(document.activeElement).toBe(feedRows[0]);
  });

  it('keeps card rows out of the Tab order — the feed region is the single tab stop', () => {
    const { container } = renderShell();
    const feed = container.querySelector('[data-role="desktop-feed"]');
    expect(feed).toHaveAttribute('tabindex', '0');
    Array.from(container.querySelectorAll<HTMLElement>('[data-role="desktop-feed-row"]')).forEach(
      (row) => {
        expect(row).toHaveAttribute('tabindex', '-1');
      },
    );
  });

  it('draws the last-seen divider, and paging older history is a plain button', () => {
    const onLoadMoreHistory = vi.fn();
    const older = makeFeedCard({
      kind: 'update',
      id: 'c4',
      label: 'older',
      body: 'from yesterday',
      createdAt: '2026-08-03T11:00:00Z',
      notificationId: 4,
    });
    const { container } = renderShell({
      feed: makeFeedSnapshot({ cards: [updateCard, older], hasMoreHistory: true }),
      lastSeenId: 5,
      hasMoreHistory: true,
      onLoadMoreHistory,
    });

    expect(container.querySelector('[data-role="feed-divider"]')).not.toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Show earlier updates' }));
    expect(onLoadMoreHistory).toHaveBeenCalled();
  });

  it('has no swipe wrapper and no bulk clear — dismissal is not ported to the desk (13 §6, Q3)', () => {
    const { container } = renderShell();
    expect(container.querySelector('[data-role="swipe-to-dismiss"]')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Clear all notifications' })).toBeNull();
  });

  it('rests in the warm near-black while mounted, and restores the theme on unmount', () => {
    const { unmount } = renderShell();
    expect(document.body.dataset.theme).toBe('dark');
    unmount();
    expect(document.body.dataset.theme).toBeUndefined();
  });

  it('publishes which shell is up, so the portaled sheet can be styled without a second breakpoint', () => {
    // The ticket-detail sheet portals to document.body, outside this subtree, so
    // no descendant selector reaches it. This attribute carries the JS shell
    // decision to it — see the `body[data-shell='desktop']` rules in
    // DesktopScreen.css. Restored on unmount so narrowing the window back to the
    // mobile shell leaves the sheet its phone geometry.
    const { unmount } = renderShell();
    expect(document.body.dataset.shell).toBe('desktop');
    unmount();
    expect(document.body.dataset.shell).toBeUndefined();
  });

  it('renders the composer it is handed, under the feed', () => {
    const { container } = renderShell({ composer: <div data-role="test-composer" /> });
    const region = container.querySelector('[data-role="desktop-composer-region"]');
    expect(region?.querySelector('[data-role="test-composer"]')).not.toBeNull();
  });
});
