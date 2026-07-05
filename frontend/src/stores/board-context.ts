// Split from board-store.tsx so that file exports only the `BoardProvider`
// component (react-refresh/only-export-components) — this file carries the
// context, its value shape, and the consumer hook.
import { createContext, useContext } from 'react';
import type { Board, ConnectionState } from '@/transport/transport';

export type { Board };

export interface BoardStoreValue {
  /** The latest snapshot, or `null` before the first `board` event/fetch arrives. */
  board: Board | null;
  /** Stream state for the connection chip (07 §8). */
  connectionState: ConnectionState;
  /** On-demand `GET /api/board`, independent of the stream: pulls the current
   * snapshot without waiting for an agent-driven `board` push. The streams
   * dropdown fires this on open so it can't sit blank/stale until the next
   * emission. */
  refreshBoard: () => void;
  /** True while a `refreshBoard()` fetch is in flight — lets a consumer show a
   * loading affordance distinct from a genuinely empty snapshot. */
  refreshing: boolean;
}

export const BoardStoreContext = createContext<BoardStoreValue | undefined>(undefined);

export function useBoardStore(): BoardStoreValue {
  const context = useContext(BoardStoreContext);
  if (context === undefined) {
    throw new Error('useBoardStore must be used within a BoardProvider');
  }
  return context;
}
