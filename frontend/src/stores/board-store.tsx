// Board store (07 §5): holds the latest `Board` snapshot. Every `board` SSE
// event — and the initial `GET /api/board` — replaces it wholesale. No
// merging, no diffing (04 D7). Live updates ride the single app-wide stream
// connection (`@/stores/stream-connection`), not a private `EventSource` of
// its own — see that module for why.
import { useCallback, useEffect, useMemo, useRef, useState, type JSX, type ReactNode } from 'react';
import { fetchBoard } from '@/transport/transport';
import type { Board, ConnectionState } from '@/transport/transport';
import { BoardStoreContext, type BoardStoreValue } from '@/stores/board-context';
import { subscribeStream } from '@/stores/stream-connection';

export interface BoardProviderProps {
  children: ReactNode;
}

export function BoardProvider({ children }: BoardProviderProps): JSX.Element {
  const [board, setBoard] = useState<Board | null>(null);
  const [connectionState, setConnectionState] = useState<ConnectionState>('connecting');
  const [refreshing, setRefreshing] = useState(false);
  // Guards against overlapping refreshes (e.g. rapid open/close of the dropdown):
  // a second call while one is in flight is a no-op rather than a second fetch.
  const refreshingRef = useRef(false);

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
        setBoard(await fetchBoard());
      } catch {
        // Swallowed: keep the last snapshot; the stream resyncs on its next event.
      } finally {
        refreshingRef.current = false;
        setRefreshing(false);
      }
    }

    void pull();
  }, []);

  // First paint: fetch the current snapshot directly (07 §5). The stream's
  // first `board` event (below) is what carries the client through any
  // reconnect after that.
  useEffect(() => {
    let cancelled = false;

    async function loadInitialBoard(): Promise<void> {
      try {
        const initialBoard = await fetchBoard();
        if (!cancelled) {
          setBoard(initialBoard);
        }
      } catch {
        // Swallowed: the stream's first `board` event still resyncs once the
        // connection opens (07 §8) — a failed first paint isn't fatal.
      }
    }

    void loadInitialBoard();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(
    () =>
      subscribeStream({
        onBoard: setBoard,
        onSay: () => {
          // The board store doesn't care about chat replies.
        },
        onConnectionStateChange: setConnectionState,
      }),
    [],
  );

  const value = useMemo<BoardStoreValue>(
    () => ({ board, connectionState, refreshBoard, refreshing }),
    [board, connectionState, refreshBoard, refreshing],
  );

  return <BoardStoreContext.Provider value={value}>{children}</BoardStoreContext.Provider>;
}
