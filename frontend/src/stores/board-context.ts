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
}

const { Context: BoardStoreContext, useStore: useBoardStore } =
  createStoreContext<BoardStoreValue>('useBoardStore');

export { BoardStoreContext, useBoardStore };
