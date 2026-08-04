// The per-project snapshot cache (12 §4.1). The stores' own tests cover what a
// switch LOOKS like; these cover the container's contract — separation by
// project, no-op without one, and the bound that keeps it from growing forever.
import { afterEach, describe, expect, it } from 'vitest';
import {
  cacheBoard,
  cacheFeed,
  readCachedBoard,
  readCachedFeed,
  resetProjectCache,
  type CachedFeed,
} from '@/stores/project-cache';
import { makeBoard, makeFeedSnapshot, makeTicket } from '@/test/fixtures';

function boardWith(id: string) {
  return makeBoard({
    working: [
      makeTicket({
        id,
        title: id,
        body: '',
        state: 'working',
        priority: 0,
        createdAt: '2026-07-01T00:00:00Z',
        updatedAt: '2026-07-01T00:00:00Z',
      }),
    ],
  });
}

function feedFor(projectId: string): CachedFeed {
  // Tagged with the project id so a case can prove which project's entry came
  // back, without needing a whole distinct feed per project.
  const hiddenTickets: [string, number][] = [[projectId, 1]];
  return {
    server: makeFeedSnapshot(),
    updates: [],
    lastSeen: null,
    acked: 0,
    dismissed: [],
    hiddenTickets,
  };
}

describe('project-cache', () => {
  afterEach(() => {
    resetProjectCache();
  });

  it('hands each project back its own snapshot, never another one’s', () => {
    cacheBoard('p-alpha', boardWith('alpha'));
    cacheBoard('p-beta', boardWith('beta'));

    expect(readCachedBoard('p-alpha')?.working[0]?.id).toBe('alpha');
    expect(readCachedBoard('p-beta')?.working[0]?.id).toBe('beta');
    // A project never written is a miss, not a fallback to the nearest thing.
    expect(readCachedBoard('p-gamma')).toBeNull();
  });

  it('is a no-op with no project resolved, rather than sharing one bucket', () => {
    // A null id happens before the session resolves a project (or after the
    // current one is deleted). Keying those together would serve one project's
    // board to the next — the one failure this cache must not have.
    cacheBoard(null, boardWith('nobody'));
    expect(readCachedBoard(null)).toBeNull();
  });

  it('keeps board and feed independent per project', () => {
    cacheFeed('p-alpha', feedFor('p-alpha'));
    expect(readCachedFeed('p-alpha')?.hiddenTickets[0]?.[0]).toBe('p-alpha');
    expect(readCachedFeed('p-beta')).toBeNull();
    expect(readCachedBoard('p-alpha')).toBeNull();
  });

  it('is bounded, dropping the least recently written project first', () => {
    // Nine projects into a cache that holds eight: the first one written is the
    // one that pays, and every project since is still served.
    for (let i = 0; i < 9; i += 1) {
      cacheBoard(`p-${String(i)}`, boardWith(`t-${String(i)}`));
    }
    expect(readCachedBoard('p-0')).toBeNull();
    expect(readCachedBoard('p-1')?.working[0]?.id).toBe('t-1');
    expect(readCachedBoard('p-8')?.working[0]?.id).toBe('t-8');
  });

  it('re-writing a project makes it recent again, so an active one is not evicted', () => {
    for (let i = 0; i < 8; i += 1) {
      cacheBoard(`p-${String(i)}`, boardWith(`t-${String(i)}`));
    }
    // p-0 is next in line to be evicted — until its stream pushes a new board.
    cacheBoard('p-0', boardWith('t-0-fresh'));
    cacheBoard('p-new', boardWith('t-new'));

    expect(readCachedBoard('p-0')?.working[0]?.id).toBe('t-0-fresh');
    expect(readCachedBoard('p-1')).toBeNull();
  });
});
