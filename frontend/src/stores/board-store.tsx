// Board store (07 §5): holds the latest `Board` snapshot. Every `board` SSE
// event — and the initial `GET /api/board` — replaces it wholesale. No
// merging, no diffing (04 D7). Live updates ride the single app-wide stream
// connection (`@/stores/stream-connection`), not a private `EventSource` of
// its own — see that module for why.
import { useEffect, useMemo, useState, type JSX, type ReactNode } from 'react';
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
    () => ({ board, connectionState }),
    [board, connectionState],
  );

  return <BoardStoreContext.Provider value={value}>{children}</BoardStoreContext.Provider>;
}
