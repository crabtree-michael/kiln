// Unit tests for the Streams status derivation (amended 2026-07-05): the real
// per-worker session status is joined from board.agents by ticket id, with the
// board-column default as the fallback before a status has arrived.
import { describe, expect, it } from 'vitest';
import { streamStatuses, streamStatusLabel, type StreamState } from '@/components/feed-format';
import { makeAgentStatus, makeBoard, makeTicket } from '@/test/fixtures';

const baseFields = { createdAt: '2026-07-01T00:00:00Z', updatedAt: '2026-07-01T00:00:00Z' };

const working = (id: string, title: string): ReturnType<typeof makeTicket> =>
  makeTicket({ ...baseFields, id, title, body: '', state: 'working', priority: 0 });

describe('streamStatuses', () => {
  it('returns [] before the first board snapshot', () => {
    expect(streamStatuses(null)).toEqual([]);
  });

  it('uses the real session status from board.agents, keyed by ticket id', () => {
    const board = makeBoard({
      working: [working('t1', 'Auth'), working('t2', 'Search')],
      agents: [makeAgentStatus('t1', 'stopped'), makeAgentStatus('t2', 'errored')],
    });
    const streams = streamStatuses(board);
    expect(streams.map((s) => [s.id, s.status])).toEqual([
      ['t1', 'stopped'],
      ['t2', 'errored'],
    ]);
  });

  it('falls back to the column default when no agent entry has arrived yet', () => {
    const board = makeBoard({
      working: [working('t1', 'Auth')],
      blocked: [
        makeTicket({
          ...baseFields,
          id: 'b1',
          title: 'Billing',
          body: '',
          state: 'blocked',
          priority: 0,
          blockedReason: 'which gateway?',
        }),
      ],
      agents: [], // none reported yet
    });
    const streams = streamStatuses(board);
    expect(streams[0]).toMatchObject({ id: 't1', status: 'building', reason: null });
    expect(streams[1]).toMatchObject({ id: 'b1', status: 'idle', reason: 'which gateway?' });
  });

  it('lists working streams before blocked ones', () => {
    const board = makeBoard({
      working: [working('t1', 'Auth')],
      blocked: [
        makeTicket({
          ...baseFields,
          id: 'b1',
          title: 'Billing',
          body: '',
          state: 'blocked',
          priority: 0,
        }),
      ],
    });
    expect(streamStatuses(board).map((s) => s.id)).toEqual(['t1', 'b1']);
  });
});

describe('streamStatusLabel', () => {
  it('renders a human label for every session state', () => {
    const labels: [StreamState, string][] = [
      ['building', 'Building'],
      ['idle', 'Idle'],
      ['stopped', 'Stopped'],
      ['errored', 'Errored'],
      ['starting', 'Starting'],
    ];
    for (const [state, label] of labels) {
      expect(streamStatusLabel(state)).toBe(label);
    }
  });
});
