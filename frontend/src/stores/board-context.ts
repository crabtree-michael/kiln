// Split from board-store.tsx so that file exports only the `BoardProvider`
// component (react-refresh/only-export-components) — this file carries the
// context, its value shape, and the consumer hook.
import type { Board, ConnectionState } from '@/transport/transport';
import { createStoreContext } from '@/stores/create-store-context';

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

const { Context: BoardStoreContext, useStore: useBoardStore } =
  createStoreContext<BoardStoreValue>('useBoardStore');

export { BoardStoreContext, useBoardStore };
