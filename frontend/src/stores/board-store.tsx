// Board store (07 §5): holds the latest `Board` snapshot. Every `board` SSE
// event — and the initial `GET /api/board` — replaces it wholesale. No
// merging, no diffing (04 D7). Live updates ride the single app-wide stream
// connection (`@/stores/stream-connection`), not a private `EventSource` of
// its own — see that module for why.
//
// One provider instance serves exactly one project: `CurrentProjectProvider`
// keys its subtree by `project_id`, so a switch remounts this store against the
// new project's stream (12 §4.1). The snapshot it holds is therefore written
// through to — and seeded back from — the per-project cache, so returning to a
// project paints its last known board at once instead of blanking for a
// round-trip. See `@/stores/project-cache`.
import { useCallback, useEffect, useMemo, useRef, useState, type JSX, type ReactNode } from 'react';
import { fetchBoard, getActiveProjectId } from '@/transport/transport';
import type { Board, ConnectionState } from '@/transport/transport';
import { BoardStoreContext, type BoardStoreValue } from '@/stores/board-context';
import { cacheBoard, readCachedBoard } from '@/stores/project-cache';
import { subscribeStream } from '@/stores/stream-connection';

export interface BoardProviderProps {
  children: ReactNode;
}

export function BoardProvider({ children }: BoardProviderProps): JSX.Element {
  // The project this instance is scoped to, captured once at mount. It cannot
  // change under us — a switch remounts the whole subtree — so reading it in a
  // lazy initializer is both correct and stable for the callbacks below.
  const [projectId] = useState(getActiveProjectId);
  // Seeded from the cache so a switch back to a project already loaded this
  // session paints its board immediately. `null` on a genuine first visit.
  const [board, setBoard] = useState<Board | null>(() => readCachedBoard(projectId));
  const [connectionState, setConnectionState] = useState<ConnectionState>('connecting');
  const [refreshing, setRefreshing] = useState(false);
  // True until this project's own snapshot lands — including the refresh that
  // runs behind a cache-seeded board, so a consumer can say "catching up"
  // rather than presenting stale data as current (13 §10: state it, don't hide
  // it). Distinct from an empty board, which is a fact rather than a wait.
  const [loading, setLoading] = useState(true);
  // Guards against overlapping refreshes (e.g. rapid open/close of the dropdown):
  // a second call while one is in flight is a no-op rather than a second fetch.
  const refreshingRef = useRef(false);

  // The single write path for the snapshot: every source (mount fetch, on-demand
  // pull, stream event) goes through here so the cache can never fall out of
  // step with what is on screen — and so any snapshot arriving is enough to end
  // the loading state, whichever route brought it.
  const applyBoard = useCallback(
    (next: Board): void => {
      cacheBoard(projectId, next);
      setBoard(next);
      setLoading(false);
    },
    [projectId],
  );

  // On-demand snapshot pull, independent of the stream (07 §5). Unlike the
  // mount-time fetch below, this can run any time a consumer asks — the streams
  // dropdown fires it on open so it reflects current state without waiting for
  // the next agent-driven `board` event. A failed pull leaves the last snapshot
  // in place; the stream still resyncs on its next event.
  const refreshBoard = useCallback((): void => {
    if (refreshingRef.current) {
      return;
    }
    refreshingRef.current = true;
    setRefreshing(true);

    async function pull(): Promise<void> {
      try {
        applyBoard(await fetchBoard());
      } catch {
        // Swallowed: keep the last snapshot; the stream resyncs on its next event.
      } finally {
        refreshingRef.current = false;
        setRefreshing(false);
      }
    }

    void pull();
  }, [applyBoard]);

  // First paint: fetch the current snapshot directly (07 §5). The stream's
  // first `board` event (below) is what carries the client through any
  // reconnect after that. This runs even when the cache seeded a board above —
  // the point of the cache is to fill the wait, never to replace the fetch.
  useEffect(() => {
    let cancelled = false;

    async function loadInitialBoard(): Promise<void> {
      try {
        const initialBoard = await fetchBoard();
        if (!cancelled) {
          applyBoard(initialBoard);
        }
      } catch {
        // Swallowed: the stream's first `board` event still resyncs once the
        // connection opens (07 §8) — a failed first paint isn't fatal.
      } finally {
        if (!cancelled) {
          // Drop the loading state either way: a failed fetch is no longer a
          // fetch in flight, and leaving the indication up would claim work
          // that isn't happening. Whatever the cache seeded stays on screen.
          setLoading(false);
        }
      }
    }

    void loadInitialBoard();
    return () => {
      cancelled = true;
    };
  }, [applyBoard]);

  useEffect(
    () =>
      subscribeStream({
        onBoard: applyBoard,
        onSay: () => {
          // The board store doesn't care about chat replies.
        },
        onConnectionStateChange: setConnectionState,
      }),
    [applyBoard],
  );

  const value = useMemo<BoardStoreValue>(
    () => ({ board, connectionState, refreshBoard, refreshing, loading }),
    [board, connectionState, refreshBoard, refreshing, loading],
  );

  return <BoardStoreContext.Provider value={value}>{children}</BoardStoreContext.Provider>;
}
