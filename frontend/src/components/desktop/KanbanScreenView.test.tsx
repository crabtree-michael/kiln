// The `/kanban` board view. Presentational, so it renders straight from
// fixtures with no stores behind it. The cases below are the brief's claims made
// checkable: the rail is the desktop app's own, the middle is five columns of
// board state, a card opens the SAME ticket sheet the feed opens (at the desk's
// right edge), and nothing on this board can be dragged.
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { KanbanScreenView } from '@/components/desktop/KanbanScreenView';
import type { RailProject } from '@/components/desktop/ProjectsRail';
import type { VoiceStoreValue } from '@/voice/voice-context';
import { makeAgentStatus, makeBoard, makeTicket } from '@/test/fixtures';

// The sheet's voice cluster and transcript are live voice-store consumers (09).
// These tests render the view directly (no `VoiceProvider`), so `useVoice` is
// mocked at the real resting state — `paused`, which is what `initialVoiceState`
// gives and is load-bearing rather than cosmetic: a `listening` mock would put
// every sheet in this file into the speaking arrangement and Accept would vanish
// from cases that never mention voice. Same stance as DesktopScreenView's.
let mockVoiceValue: VoiceStoreValue;

function restingVoice(): VoiceStoreValue {
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

function ticket(
  id: string,
  title: string,
  state: 'shaping' | 'ready' | 'working' | 'blocked' | 'done',
  extra: { blockedReason?: string; statusChangedAt?: string } = {},
) {
  return makeTicket({
    id,
    title,
    body: 'the full record of the work',
    state,
    priority: 1,
    createdAt: '2026-08-04T09:00:00Z',
    updatedAt: '2026-08-04T11:00:00Z',
    statusChangedAt: extra.statusChangedAt ?? '2026-08-04T11:00:00Z',
    ...(extra.blockedReason === undefined ? {} : { blockedReason: extra.blockedReason }),
  });
}

const FULL_BOARD = makeBoard({
  shaping: [ticket('t1', 'rate limit the poller', 'shaping')],
  ready: [ticket('t2', 'retry rotated tokens', 'ready')],
  working: [ticket('t3', 'auth refresh', 'working')],
  blocked: [
    ticket('t4', 'schema migration', 'blocked', {
      blockedReason: 'Do you want the old column dropped, or kept until the backfill lands?',
    }),
  ],
  done: [ticket('t5', 'ship the landing page', 'done')],
  agents: [makeAgentStatus('t3', 'building'), makeAgentStatus('t4', 'idle')],
});

function renderView(props: Partial<Parameters<typeof KanbanScreenView>[0]> = {}) {
  return render(
    <KanbanScreenView
      projects={PROJECTS}
      currentProjectId="p1"
      onSelectProject={vi.fn()}
      board={FULL_BOARD}
      connectionState="connected"
      onAccept={vi.fn()}
      now={NOW}
      {...props}
    />,
  );
}

/** The cards of one named column, in render order. */
function cardsIn(label: string): HTMLElement[] {
  const column = screen.getByRole('region', { name: new RegExp(`^${label},`) });
  return within(column).queryAllByRole('button');
}

/** One card of a named column. Throws rather than asserting a type: the escape
 * hatches (`as`, `!`) are banned by the gate (02 §4b), and a missing card should
 * fail with a sentence rather than as `undefined` three lines later. */
function cardIn(label: string, index = 0): HTMLElement {
  const card = cardsIn(label)[index];
  if (card === undefined) {
    throw new Error(`no card at index ${index.toString()} in the ${label} column`);
  }
  return card;
}

beforeEach(() => {
  mockVoiceValue = restingVoice();
  delete document.body.dataset.shell;
});

describe('KanbanScreenView', () => {
  it('renders the desktop shell in its kanban reading', () => {
    const { container } = renderView();
    const shell = container.querySelector("[data-role='desktop-screen']");
    // The root IS the desktop shell — it wears the same role so every
    // shell-scoped rule (viewport lock, box model, the bell's anchoring at the
    // rail's foot) applies here too — and `data-view` is the only difference.
    expect(shell).not.toBeNull();
    expect(shell?.getAttribute('data-view')).toBe('kanban');
  });

  it('carries the desktop app’s own rail, not a lookalike', () => {
    // The brief: "the project sidebar is identical to the current desktop app's".
    // The guarantee is structural — `DesktopRail` is one component mounted by
    // both views — so this asserts the parts that prove it is that component:
    // the wordmark, one row per project with the current one marked, and the
    // foot's actions.
    const { container } = renderView();
    expect(container.querySelector("[data-role='desktop-rail']")).not.toBeNull();
    expect(screen.getByText('Kiln')).toBeInTheDocument();
    const rows = container.querySelectorAll("[data-role='rail-project']");
    expect(Array.from(rows).map((row) => row.textContent)).toEqual(['kiln', 'atlasneeds you']);
    expect(rows[0]?.getAttribute('aria-current')).toBe('true');
    expect(container.querySelector("[data-role='rail-dashboard']")).not.toBeNull();
    expect(container.querySelector("[data-role='rail-new']")).not.toBeNull();
  });

  it('switches project from the rail exactly as the feed shell does', () => {
    const onSelectProject = vi.fn();
    renderView({ onSelectProject });
    fireEvent.click(screen.getByRole('button', { name: /atlas/ }));
    expect(onSelectProject).toHaveBeenCalledWith('p2');
  });

  it('lays the board out as five columns in pipeline order', () => {
    const { container } = renderView();
    const columns = container.querySelectorAll("[data-role='kanban-column']");
    expect(Array.from(columns).map((column) => column.getAttribute('data-column'))).toEqual([
      'shaping',
      'ready',
      'working',
      'blocked',
      'done',
    ]);
    expect(
      Array.from(container.querySelectorAll("[data-role='kanban-column-label']")).map(
        (head) => head.textContent,
      ),
    ).toEqual(['Shaping', 'Ready', 'Working', 'Blocked', 'Done']);
  });

  it('puts every ticket in the column its state names', () => {
    renderView();
    expect(cardsIn('Shaping').map((card) => card.getAttribute('data-ticket-id'))).toEqual(['t1']);
    expect(cardsIn('Ready').map((card) => card.getAttribute('data-ticket-id'))).toEqual(['t2']);
    expect(cardsIn('Working').map((card) => card.getAttribute('data-ticket-id'))).toEqual(['t3']);
    expect(cardsIn('Blocked').map((card) => card.getAttribute('data-ticket-id'))).toEqual(['t4']);
    expect(cardsIn('Done').map((card) => card.getAttribute('data-ticket-id'))).toEqual(['t5']);
  });

  it('states a column’s depth in its count and in its accessible name', () => {
    const { container } = renderView({
      board: makeBoard({
        ready: [ticket('t1', 'one', 'ready'), ticket('t2', 'two', 'ready')],
      }),
    });
    const counts = Array.from(container.querySelectorAll("[data-role='kanban-column-count']")).map(
      (count) => count.textContent,
    );
    expect(counts).toEqual(['0', '2', '0', '0', '0']);
    expect(screen.getByRole('region', { name: 'Ready, 2 tickets' })).toBeInTheDocument();
  });

  it('shows a blocked ticket’s reason on the card itself', () => {
    // A blocker is a question waiting on a person; making someone open the ticket
    // to discover there IS a question defeats the point of a board.
    const { container } = renderView();
    const reason = container.querySelector("[data-role='kanban-card-reason']");
    expect(reason?.textContent).toBe(
      'Do you want the old column dropped, or kept until the backfill lands?',
    );
    // …and only there: a ticket in any other state carries no body text.
    expect(container.querySelectorAll("[data-role='kanban-card-reason']")).toHaveLength(1);
  });

  it('marks a card with its real session status, and only where one exists', () => {
    const { container } = renderView();
    const dots = container.querySelectorAll("[data-role='kanban-card'] [data-role='status-dot']");
    // Two agents are bound (t3 building, t4 idle); the three tickets with no
    // worker behind them get no mark at all rather than an invented one.
    expect(Array.from(dots).map((dot) => dot.getAttribute('data-status'))).toEqual([
      'building',
      'idle',
    ]);
    // …and each mark is coloured by the card's own column, not by that session:
    // the working card wears the ember its detail sheet wears, and only the
    // blocked one reaches for fire (the shared mark's tokens, PrimaryScreen.css).
    expect(Array.from(dots).map((dot) => dot.getAttribute('data-state'))).toEqual([
      'working',
      'blocked',
    ]);
  });

  it('reports a stopped session rather than the column it sits in', () => {
    // The board's Working bucket says "this ticket holds a worker"; the agents
    // join says whether that worker is actually running. The card must say the
    // second thing.
    renderView({
      board: makeBoard({
        working: [ticket('t3', 'auth refresh', 'working')],
        agents: [makeAgentStatus('t3', 'stopped')],
      }),
    });
    expect(within(cardIn('Working')).getByText('stopped')).toBeInTheDocument();
  });

  it('says how long a ticket has been where it is', () => {
    renderView({
      board: makeBoard({
        ready: [
          ticket('t2', 'retry rotated tokens', 'ready', {
            statusChangedAt: '2026-08-04T09:00:00Z',
          }),
        ],
      }),
    });
    // 3h in Ready, from `state_changed_at` — time in THIS state, not time since
    // the last edit.
    expect(within(cardIn('Ready')).getByText('3h')).toBeInTheDocument();
  });

  it('keeps an empty column present, with its heading and no apology', () => {
    const { container } = renderView({ board: makeBoard({}) });
    expect(container.querySelectorAll("[data-role='kanban-column']")).toHaveLength(5);
    expect(container.querySelectorAll("[data-role='kanban-column-empty']")).toHaveLength(5);
    expect(screen.queryByText(/nothing here/i)).not.toBeInTheDocument();
  });

  it('renders the five columns before the first snapshot lands', () => {
    const { container } = renderView({ board: null, loading: true });
    expect(container.querySelectorAll("[data-role='kanban-column']")).toHaveLength(5);
    expect(screen.getByRole('status')).toHaveTextContent('Loading…');
  });

  it('states the project-switch wait and drops it when the board lands', () => {
    const { rerender } = renderView({ loading: true });
    expect(screen.getByRole('status')).toHaveTextContent('Loading…');
    rerender(
      <KanbanScreenView
        projects={PROJECTS}
        currentProjectId="p1"
        onSelectProject={vi.fn()}
        board={FULL_BOARD}
        connectionState="connected"
        onAccept={vi.fn()}
        now={NOW}
      />,
    );
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
  });

  it('states a dropped stream at the rail’s foot', () => {
    // 13 §10: an ambient app that has silently stopped receiving is worse than
    // one that is visibly off. Same notice, same place, as the feed shell.
    renderView({ connectionState: 'reconnecting' });
    expect(screen.getByText(/Reconnecting/)).toBeInTheDocument();
  });

  it('has no drag affordance anywhere on the board', () => {
    // 07 D5: every transition belongs to the brain. A draggable column would be
    // a second, contradicting source of truth about the same five states.
    const { container } = renderView();
    expect(container.querySelector('[draggable]')).toBeNull();
    expect(container.querySelector('[data-role="kanban-card"][aria-grabbed]')).toBeNull();
  });

  describe('opening a card', () => {
    it('opens the ticket sheet, at the desk’s right edge', async () => {
      renderView();
      fireEvent.click(cardIn('Blocked'));
      const sheet = await screen.findByRole('dialog', { name: 'schema migration' });
      expect(sheet).toBeInTheDocument();
      // The panel edge is vaul's `direction`, set once from `placement="right"`
      // — never restated in CSS, which vaul's inline transforms would beat.
      expect(sheet.closest('[data-vaul-drawer-direction="right"]')).not.toBeNull();
      // …and the stamp the portaled panel's geometry is found by.
      expect(document.body.dataset.shell).toBe('desktop');
    });

    it('shows the full blocked reason the card clamped', async () => {
      renderView();
      fireEvent.click(cardIn('Blocked'));
      const sheet = await screen.findByRole('dialog', { name: 'schema migration' });
      expect(
        within(sheet).getByText(
          'Do you want the old column dropped, or kept until the backfill lands?',
        ),
      ).toBeInTheDocument();
    });

    it('accepts a shaping proposal from the sheet and closes it', async () => {
      const onAccept = vi.fn();
      renderView({ onAccept });
      fireEvent.click(cardIn('Shaping'));
      const sheet = await screen.findByRole('dialog', { name: 'rate limit the poller' });
      fireEvent.click(within(sheet).getByRole('button', { name: /accept/i }));
      expect(onAccept).toHaveBeenCalledWith('t1');
    });

    it('closes on Escape', async () => {
      renderView();
      fireEvent.click(cardIn('Done'));
      await screen.findByRole('dialog', { name: 'ship the landing page' });
      fireEvent.keyDown(document, { key: 'Escape' });
      await vi.waitFor(() => {
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      });
    });
  });
});
