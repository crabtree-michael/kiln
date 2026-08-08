// Unit tests for the shared ticket intents (shell-architecture plan, T4).
//
// Two things are worth pinning here beyond "it calls the right endpoint":
//
//  1. **The injected optimistic hides.** Plan §7 decided that the feed's
//     instant card-vanish reaches this hook as an INPUT rather than being looked
//     up ambiently, so that "does this route have a feed?" stays legible at the
//     route. The tests below cover both wirings — with the hides (what
//     `PrimaryScreen` passes) and without them (what `KanbanScreen` passes) —
//     because the no-feed case is precisely the one an ambient lookup would have
//     made invisible.
//  2. **Callback identity.** `FeedCardItem` and `TicketDetail` re-render off
//     callback identity, so a hook that returned fresh functions each render
//     would quietly change re-render behaviour across every screen. Plan §5
//     called for one explicit test; it is the last block.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useTicketActions, type TicketActions } from '@/components/ticket-intents';
import * as transport from '@/transport/transport';

vi.mock('@/transport/transport', () => ({
  acceptTicket: vi.fn(),
  deleteTicket: vi.fn(),
  editTicketText: vi.fn(),
  killTicketSandbox: vi.fn(),
  postMessage: vi.fn(),
  reassignTicketSandbox: vi.fn(),
  setTicketSandbox: vi.fn(),
}));

const mocked = vi.mocked(transport);

/** What the three brain-routed endpoints resolve with — the queued event and the
 * message the brain will wake on. Nothing here reads it; it exists so the mocks
 * keep the real return types. */
const POSTED = { event_id: 1, message_id: 1 };

/** Every transport call this hook can make, resolved, so the default is a write
 * that lands. The failure-recovery tests override the one they care about. */
function resolveAll(): void {
  mocked.acceptTicket.mockResolvedValue(POSTED);
  mocked.deleteTicket.mockResolvedValue(POSTED);
  mocked.postMessage.mockResolvedValue(POSTED);
  mocked.editTicketText.mockResolvedValue(undefined);
  mocked.killTicketSandbox.mockResolvedValue(undefined);
  mocked.reassignTicketSandbox.mockResolvedValue(undefined);
  mocked.setTicketSandbox.mockResolvedValue(undefined);
}

/** The seven intents, by name — the contract every screen consumes. */
const ACTION_KEYS: (keyof TicketActions)[] = [
  'onAccept',
  'onDelete',
  'onPoke',
  'onSetKeepSandbox',
  'onKillSandbox',
  'onReassignSandbox',
  'onEditText',
];

/** Mounts the hook the way `KanbanScreen` does — no feed, so no hides. */
function mountWithoutFeed(refreshBoard = vi.fn()): {
  actions: TicketActions;
  refreshBoard: ReturnType<typeof vi.fn>;
} {
  const { result } = renderHook(() => useTicketActions({ refreshBoard }));
  return { actions: result.current, refreshBoard };
}

beforeEach(() => {
  resolveAll();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('the intents that route through the brain', () => {
  it('accepts by hitting the accept endpoint', () => {
    mountWithoutFeed().actions.onAccept('t1');
    expect(mocked.acceptTicket).toHaveBeenCalledWith('t1');
  });

  it('deletes by hitting the delete endpoint', () => {
    mountWithoutFeed().actions.onDelete('t1');
    expect(mocked.deleteTicket).toHaveBeenCalledWith('t1');
  });

  it('pokes as a human message naming the ticket, never as a direct agent command', () => {
    // D5: the client cannot command an agent — send_to_agent is a brain tool —
    // so the poke has to be something the brain can resolve.
    mountWithoutFeed().actions.onPoke('t1');
    expect(mocked.postMessage).toHaveBeenCalledWith('Poke the agent on ticket t1 to continue.');
  });
});

describe('the direct writes', () => {
  it('writes the sandbox option straight to the board', () => {
    mountWithoutFeed().actions.onSetKeepSandbox('t1', true);
    expect(mocked.setTicketSandbox).toHaveBeenCalledWith('t1', true);
  });

  it('kills and reassigns a sandbox directly', () => {
    const { actions } = mountWithoutFeed();
    actions.onKillSandbox('t1');
    actions.onReassignSandbox('t2');
    expect(mocked.killTicketSandbox).toHaveBeenCalledWith('t1');
    expect(mocked.reassignTicketSandbox).toHaveBeenCalledWith('t2');
  });

  it('sends the typed text verbatim, with no brain pass in between', () => {
    mountWithoutFeed().actions.onEditText('t1', { title: 'Fixed wording' });
    expect(mocked.editTicketText).toHaveBeenCalledWith('t1', { title: 'Fixed wording' });
  });

  it.each([
    [
      'onSetKeepSandbox',
      (a: TicketActions) => {
        a.onSetKeepSandbox('t1', true);
      },
      'setTicketSandbox',
    ],
    [
      'onKillSandbox',
      (a: TicketActions) => {
        a.onKillSandbox('t1');
      },
      'killTicketSandbox',
    ],
    [
      'onReassignSandbox',
      (a: TicketActions) => {
        a.onReassignSandbox('t1');
      },
      'reassignTicketSandbox',
    ],
    [
      'onEditText',
      (a: TicketActions) => {
        a.onEditText('t1', { body: 'b' });
      },
      'editTicketText',
    ],
  ] as const)('refreshes the board when %s is refused', async (_name, fire, endpoint) => {
    // Every direct write recovers the same way: the sheet showed the change
    // optimistically, so a refusal has to snap it back at once rather than
    // leaving the user to wait out the time-box.
    mocked[endpoint].mockRejectedValue(new Error('409'));
    const refreshBoard = vi.fn();
    fire(mountWithoutFeed(refreshBoard).actions);
    await vi.waitFor(() => {
      expect(refreshBoard).toHaveBeenCalledTimes(1);
    });
  });

  it('does not refresh the board when a direct write lands', async () => {
    const refreshBoard = vi.fn();
    mountWithoutFeed(refreshBoard).actions.onSetKeepSandbox('t1', false);
    await vi.waitFor(() => {
      expect(mocked.setTicketSandbox).toHaveBeenCalled();
    });
    expect(refreshBoard).not.toHaveBeenCalled();
  });
});

describe('the optimistic card hides are injected, not looked up (plan §7)', () => {
  it('hides the card BEFORE the write when a screen wires one', () => {
    // The order is what makes the tap feel instant rather than merely eventual.
    const order: string[] = [];
    const onAcceptOptimistic = vi.fn(() => order.push('hide'));
    mocked.acceptTicket.mockImplementation(() => {
      order.push('write');
      return Promise.resolve(POSTED);
    });
    const { result } = renderHook(() =>
      useTicketActions({ refreshBoard: vi.fn(), onAcceptOptimistic }),
    );
    result.current.onAccept('t1');
    expect(onAcceptOptimistic).toHaveBeenCalledWith('t1');
    expect(order).toEqual(['hide', 'write']);
  });

  it('hides the deleted ticket’s card when a screen wires one', () => {
    const onDeleteOptimistic = vi.fn();
    const { result } = renderHook(() =>
      useTicketActions({ refreshBoard: vi.fn(), onDeleteOptimistic }),
    );
    result.current.onDelete('t1');
    expect(onDeleteOptimistic).toHaveBeenCalledWith('t1');
    expect(mocked.deleteTicket).toHaveBeenCalledWith('t1');
  });

  it('still writes when a screen has no feed to hide anything in', () => {
    // `/kanban`'s wiring. The hook must not require a feed — that route
    // deliberately mounts no FeedProvider, and this is the case an ambient
    // `useOptionalFeedStore()` would have made invisible at the call site.
    const { actions } = mountWithoutFeed();
    actions.onAccept('t1');
    actions.onDelete('t2');
    expect(mocked.acceptTicket).toHaveBeenCalledWith('t1');
    expect(mocked.deleteTicket).toHaveBeenCalledWith('t2');
  });
});

describe('callback identity', () => {
  it('returns the same callbacks across re-renders with unchanged inputs', () => {
    // FeedCardItem and TicketDetail re-render off callback identity, so fresh
    // functions each render would change re-render behaviour everywhere at once.
    const refreshBoard = vi.fn();
    const onAcceptOptimistic = vi.fn();
    const { result, rerender } = renderHook(() =>
      useTicketActions({ refreshBoard, onAcceptOptimistic }),
    );
    const first = result.current;
    rerender();
    expect(result.current).toBe(first);
    // Named rather than derived from `Object.keys`, so this also fails if an
    // eighth intent is added without a decision about its identity.
    expect(Object.keys(first).sort()).toEqual([...ACTION_KEYS].sort());
    for (const key of ACTION_KEYS) {
      expect(result.current[key]).toBe(first[key]);
    }
  });

  it('renews the affected callback when an input actually changes', () => {
    const { result, rerender } = renderHook(
      ({ refreshBoard }: { refreshBoard: () => void }) => useTicketActions({ refreshBoard }),
      { initialProps: { refreshBoard: vi.fn() } },
    );
    const first = result.current;
    rerender({ refreshBoard: vi.fn() });
    expect(result.current.onKillSandbox).not.toBe(first.onKillSandbox);
    // …but an intent that doesn't depend on it is untouched.
    expect(result.current.onPoke).toBe(first.onPoke);
  });
});
