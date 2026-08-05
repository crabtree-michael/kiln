// The desktop shell (13 §3–§10). Presentational, so it renders straight from
// fixtures with no stores behind it. The cases below are the document's claims
// made checkable: two regions and only two, the feed scoped to the selected
// project, the states it has to hold, detail over the feed rather than beside
// it, and a keyboard path through the column.
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { DesktopScreenView } from '@/components/desktop/DesktopScreenView';
import type { RailProject } from '@/components/desktop/ProjectsRail';
import type { VoiceStoreValue } from '@/voice/voice-context';
import {
  makeAgentStatus,
  makeBoard,
  makeFeedCard,
  makeFeedSnapshot,
  makeTicket,
} from '@/test/fixtures';

// The in-panel voice cluster and its transcript are live voice-store consumers
// (09). These presentational tests render the shell directly (no `VoiceProvider`),
// so `useVoice` is mocked to the real resting state (`paused` — the app never
// opens listening) — deterministic, and no mic/socket I/O. Mutable so the one case
// that needs a live mic can supply one; `beforeEach` puts it back at rest. Same
// stance and same shape as PrimaryScreenView.test.tsx.
let mockVoiceValue: VoiceStoreValue;

function restingVoice(overrides: Partial<VoiceStoreValue> = {}): VoiceStoreValue {
  return {
    micState: 'paused',
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
    editing: false,
    beginEdit: vi.fn(),
    editTranscript: vi.fn(),
    endEdit: vi.fn(),
    getLevel: vi.fn(() => 0),
    keyboardMode: false,
    openKeyboard: vi.fn(),
    closeKeyboard: vi.fn(),
    submitText: vi.fn(() => Promise.resolve(true)),
    setTicketContext: vi.fn(),
    ...overrides,
  };
}

vi.mock('@/voice/voice-context', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/voice/voice-context')>();
  return { ...actual, useVoice: (): VoiceStoreValue => mockVoiceValue };
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
  beforeEach(() => {
    mockVoiceValue = restingVoice();
  });

  it('is three regions — rail, in-progress panel, feed — and nothing else', () => {
    const { container } = renderShell();
    expect(container.querySelector('[data-role="desktop-rail"]')).not.toBeNull();
    expect(container.querySelector('[data-role="desktop-working-panel"]')).not.toBeNull();
    expect(container.querySelector('[data-role="desktop-feed"]')).not.toBeNull();
    // The Kanban board is not revived on desktop (13 D2).
    expect(container.querySelector('[data-role="board"]')).toBeNull();
    expect(container.querySelector('[data-role="board-column"]')).toBeNull();
    // And the new column is NOT an inspector: it holds no selection and shows no
    // detail, so ticket detail is still an overlay (13 D7).
    expect(container.querySelector('[data-role="desktop-inspector"]')).toBeNull();
  });

  it('orders the columns rail → in-progress → feed, left to right', () => {
    // The grid places by source order, so the DOM order IS the layout. A panel
    // authored after <main> would render to the right of the feed.
    const { container } = renderShell();
    const roles = Array.from(
      container.querySelectorAll<HTMLElement>('[data-role="desktop-screen"] > [data-role]'),
    ).map((node) => node.dataset.role);
    expect(roles).toEqual(['desktop-rail', 'desktop-working-panel', 'desktop-main']);
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
    // Nothing claims to be loading once the answer is in.
    expect(container.querySelector('[data-role="desktop-loading"]')).toBeNull();
  });

  it('resting: leads with the bell mark, and it is decoration the reader never hears', () => {
    // The same mark the phone's all-clear state shows, so the two shells' resting
    // views read as one app. It carries no meaning the lines beneath it don't
    // already carry, so it is hidden from assistive tech rather than described —
    // an alt of "bell" would announce a glyph in place of "All quiet."
    const { container } = renderShell({ feed: makeFeedSnapshot({ cards: [] }) });
    const mark = container.querySelector('[data-role="desktop-rest-mark"]');
    expect(mark).not.toBeNull();
    expect(mark).toHaveAttribute('aria-hidden', 'true');
    expect(mark).toHaveAttribute('alt', '');
    // It leads the block: the icon is above the words, not beside them.
    const line = container.querySelector('[data-role="desktop-rest-line"]');
    expect(mark?.compareDocumentPosition(line ?? mark)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });

  it('resting: nothing but the resting state is marked empty — a feed with cards is not', () => {
    // The attribute is what switches the region to the centred, foot-anchored
    // layout, so a populated feed carrying it would centre the card column and
    // strand "Show earlier" at the bottom of the window.
    const empty = renderShell({ feed: makeFeedSnapshot({ cards: [] }) });
    expect(feedRegion(empty.container)).toHaveAttribute('data-empty', 'true');
    empty.unmount();
    const full = renderShell();
    expect(feedRegion(full.container)).not.toHaveAttribute('data-empty');
  });

  it('resting: the last word is subtext under the count, not bulleted onto it', () => {
    // Joined onto one line the two wrapped on a narrow window; the count is the
    // fact the resting state is about, so the last word sits under it, smaller.
    const { container } = renderShell({
      feed: makeFeedSnapshot({
        cards: [],
        summary: { building: 3, last_word_at: new Date(NOW - 6 * 60_000).toISOString() },
      }),
    });
    expect(container.querySelector('[data-role="desktop-rest-detail"]')).toHaveTextContent(
      '3 building',
    );
    expect(container.querySelector('[data-role="desktop-rest-subtext"]')).toHaveTextContent(
      'last word 6m ago',
    );
    expect(screen.queryByText(/·/)).not.toBeInTheDocument();
  });

  it('resting: no subtext line at all when the brain has never spoken', () => {
    const { container } = renderShell({ feed: makeFeedSnapshot({ cards: [] }) });
    expect(container.querySelector('[data-role="desktop-rest-detail"]')).not.toBeNull();
    expect(container.querySelector('[data-role="desktop-rest-subtext"]')).toBeNull();
  });

  it('loading: says so while the project is being fetched, above the feed', () => {
    // Switching projects used to give no sign at all until a round-trip landed
    // (12 §4.1). It is stated in flow above the feed, like the working strip, so
    // it is a fact about the project rather than a card in the history.
    const { container } = renderShell({ loading: true });
    const line = container.querySelector('[data-role="desktop-loading"]');
    expect(line).not.toBeNull();
    expect(screen.getByRole('status')).toHaveTextContent('Loading…');
    const region = container.querySelector('[data-role="desktop-feed"]');
    // `compareDocumentPosition` rather than a DOM-order index: the assertion is
    // "before the feed region", not "the Nth child of main".
    expect(line?.compareDocumentPosition(region ?? line)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });

  it('loading: withholds "All quiet." until it is actually known to be true', () => {
    // The resting line is a statement, not a placeholder. Saying it mid-fetch
    // and then replacing it with three blockers teaches the user not to believe
    // the one line this screen most needs them to believe.
    const { container } = renderShell({ feed: makeFeedSnapshot({ cards: [] }), loading: true });
    expect(container.querySelector('[data-role="desktop-rest"]')).toBeNull();
    expect(container.querySelector('[data-role="desktop-loading"]')).not.toBeNull();
  });

  it('loading: cached cards stay on screen under the indication, never blanked', () => {
    // The point of the per-project cache is that a return switch shows the last
    // known feed WHILE it refreshes — a loading state that hid it would put the
    // blank frame straight back.
    const { container } = renderShell({ loading: true });
    expect(screen.getByText(/retry once with a fresh token/)).toBeInTheDocument();
    expect(container.querySelectorAll('[data-role="desktop-feed-row"]')).toHaveLength(2);
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

  it('working: the panel is BESIDE the feed, not inside it, so it cannot be scrolled away', () => {
    const { container } = renderShell({
      board: makeBoard({ working: [workingTicket('t1', 'auth refresh', '2026-08-04T11:00:00Z')] }),
      thinking: true,
    });
    const strip = container.querySelector('[data-role="desktop-working"]');
    expect(strip).not.toBeNull();
    // Its own column off the screen root — not in the feed's scroller, and not
    // in the main column above the feed either.
    expect(
      container.querySelector('[data-role="desktop-feed"] [data-role="desktop-working"]'),
    ).toBeNull();
    expect(
      container.querySelector('[data-role="desktop-main"] [data-role="desktop-working"]'),
    ).toBeNull();
    expect(strip?.parentElement).toHaveAttribute('data-role', 'desktop-working-panel');
  });

  it('working: each row carries the SAME status mark the phone uses', () => {
    // 13 §4 is "the same design language as mobile, not the same design" — and a
    // status vocabulary is language. The mark is the phone's `status-dot`, from
    // the phone's rules, so "building" cannot come to mean one thing on a desk
    // and another in a pocket.
    const { container } = renderShell({
      board: makeBoard({
        working: [
          workingTicket('t1', 'auth refresh', '2026-08-04T11:00:00Z'),
          workingTicket('t2', 'poller', '2026-08-04T11:50:00Z'),
        ],
        agents: [makeAgentStatus('t2', 'errored')],
      }),
      thinking: true,
    });
    const marks = Array.from(
      container.querySelectorAll<HTMLElement>(
        '[data-role="desktop-working-ticket"] [data-role="status-dot"]',
      ),
    ).map((node) => node.dataset.status);
    // `building` is the fallback for a ticket whose status join hasn't landed —
    // it is what the board's Working bucket already asserts.
    expect(marks).toEqual(['building', 'errored']);
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
    expect(container.querySelector('[data-role="desktop-working-head"]')).toHaveAttribute(
      'data-active',
      'true',
    );
    expect(container.querySelector('[data-role="desktop-working-list"]')).toBeNull();
  });

  it('working: a board naming a working ticket lights the strip even before the summary agrees', () => {
    const { container } = renderShell({
      board: makeBoard({ working: [workingTicket('t1', 'auth refresh', '2026-08-04T11:00:00Z')] }),
    });
    expect(container.querySelector('[data-role="desktop-working"]')).not.toBeNull();
  });

  it('working: no counts and no meters — the panel lists, it does not measure (13 §8)', () => {
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

  it('resting: the panel stays put and says nothing is running, rather than vanishing', () => {
    // The column is permanent on purpose. A panel that appeared and disappeared
    // with the work would shove the feed sideways every time an agent picked
    // something up — the loudest possible way to announce a change, on a screen
    // whose first principle is that change arrives without announcing itself.
    const { container } = renderShell();
    expect(container.querySelector('[data-role="desktop-working-panel"]')).not.toBeNull();
    expect(container.querySelector('[data-role="desktop-working-list"]')).toBeNull();
    expect(screen.getByText('Nothing in progress.')).toBeInTheDocument();
    // …and nothing about it is live: the mark does not breathe, and there is no
    // live region re-announcing an absence.
    expect(container.querySelector('[data-role="desktop-working-dot"]')).toHaveAttribute(
      'data-active',
      'false',
    );
    expect(container.querySelector('[data-role="desktop-working-head"]')).not.toHaveAttribute(
      'role',
    );
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
    const dialog = screen.getByRole('dialog', { name: 'auth refresh' });
    expect(dialog).toBeInTheDocument();
    expect(container.querySelector('[data-role="desktop-feed"]')).not.toBeNull();
    // …and it opens from the RIGHT edge, not up from the bottom (13 D7a). The
    // attribute is vaul's own record of the `direction` our `placement` prop
    // hands it, which is what drives the entrance, the closed transform and the
    // drag axis — jsdom does no layout, so this is the only thing in the gate
    // that can see the panel is anchored where the CSS expects it.
    expect(dialog.getAttribute('data-vaul-drawer-direction')).toBe('right');
  });

  // The footer swap reaches the desk on the same wiring as the phone (the sheet
  // is one component either way) — this pins that this shell passes it too, since
  // the seam is per-shell state and forgetting one is the obvious way to ship it
  // half-done.
  it('swaps the panel’s Accept for the mic’s Send and × while a voice session is live', () => {
    mockVoiceValue = restingVoice({ micState: 'listening', settledText: 'make it two columns' });
    const ticket = makeTicket({
      id: 't1',
      title: 'auth refresh',
      body: 'a proposal body',
      state: 'shaping',
      priority: 1,
      createdAt: '2026-08-04T11:00:00Z',
      updatedAt: '2026-08-04T11:00:00Z',
    });
    renderShell({
      feed: makeFeedSnapshot({
        cards: [
          makeFeedCard({
            kind: 'proposal',
            id: 'c3',
            label: 'auth refresh',
            body: 'a proposal body',
            createdAt: '2026-08-04T11:30:00Z',
            ticketId: 't1',
          }),
        ],
      }),
      board: makeBoard({ shaping: [ticket] }),
    });
    fireEvent.click(screen.getByRole('button', { name: 'Open ticket: auth refresh' }));

    // Scoped to the panel: the feed card behind it keeps its own in-place Accept,
    // which is a different affordance and stays put.
    const dialog = screen.getByRole('dialog', { name: 'auth refresh' });
    expect(within(dialog).queryByRole('button', { name: 'Accept' })).toBeNull();
    expect(within(dialog).getByRole('button', { name: 'Send' })).toBeEnabled();
    expect(within(dialog).getByRole('button', { name: 'Discard' })).toBeInTheDocument();
    expect(
      within(dialog)
        .getByRole('button', { name: 'Talk' })
        .closest('[data-role="ticket-detail-voice-actions"]'),
    ).toHaveAttribute('data-position', 'trail');
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

  it('draws the last-seen divider, and reaching back is the same one plain button (08 D2‴)', () => {
    const onShowEarlier = vi.fn();
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
      hasEarlier: true,
      onShowEarlier,
    });

    expect(container.querySelector('[data-role="feed-divider"]')).not.toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Show earlier' }));
    expect(onShowEarlier).toHaveBeenCalled();
  });

  it('keeps the control on a feed whose cards have all collapsed away (08 D2‴)', () => {
    // The desk collapses seen cards exactly like the phone, so it needs the way
    // back in exactly the state where every card has gone: the resting view.
    const { container } = renderShell({
      feed: makeFeedSnapshot({ cards: [] }),
      hasEarlier: true,
      onShowEarlier: vi.fn(),
    });
    expect(screen.getByRole('button', { name: 'Show earlier' })).toBeInTheDocument();
    // And it sits at the FOOT of the resting state, below the mark and the
    // lines — the last thing in the feed region, so the next thing under it is
    // the input. (The CSS is what pins it to the bottom edge; this pins the
    // order it has to be in for that to mean anything.)
    const region = feedRegion(container);
    expect(region.lastElementChild).toHaveAttribute('data-role', 'feed-show-earlier');
    const rest = container.querySelector('[data-role="desktop-rest"]');
    const button = region.lastElementChild;
    expect(rest?.compareDocumentPosition(button ?? rest)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });

  it('has no swipe wrapper and no bulk clear — dismissal is not ported to the desk (13 §6, Q3)', () => {
    const { container } = renderShell();
    expect(container.querySelector('[data-role="swipe-to-dismiss"]')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Clear all notifications' })).toBeNull();
  });

  it('pins no theme of its own, so the desk follows the system preference', () => {
    // This shell used to stamp `data-theme="dark"` on <body>, which beat the
    // system preference ThemeColorSync writes to <html> (13 D6). A light-mode
    // user got a dark window at their desk and a paper one on their phone. The
    // preference is the person's, not the viewport's — so the shell touches
    // `data-theme` nowhere, mounted or unmounted, and tokens.css resolves the
    // palette from <html> exactly as it does on every other route.
    document.documentElement.dataset.theme = 'light';
    const { unmount } = renderShell();
    expect(document.body.dataset.theme).toBeUndefined();
    expect(document.documentElement.dataset.theme).toBe('light');
    unmount();
    expect(document.documentElement.dataset.theme).toBe('light');
    delete document.documentElement.dataset.theme;
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
