// Composes the board region (07 §7): three columns in order — Backlog
// (Shaping over Ready), Developing (Blocked above Working), Done — plus the
// capacity chip. Board is read-only: no drag-and-drop, all mutation flows
// through chat (D5). Reads from the board store; `connectionState` also
// drives the "dim while reconnecting" treatment (07 §8), left to the
// solution phase's styling.
import type { JSX } from 'react';
import { useBoardStore } from '@/stores/board-context';
import type { ConnectionState } from '@/transport/transport';
import { BoardColumn, type BoardColumnZone } from '@/components/BoardColumn';
import { CapacityChip } from '@/components/CapacityChip';

export function Board(): JSX.Element {
  const { board, connectionState } = useBoardStore();

  const backlogZones: BoardColumnZone[] = [
    { label: 'Shaping', tickets: board?.shaping ?? [] },
    { label: 'Ready', tickets: board?.ready ?? [] },
  ];
  const developingZones: BoardColumnZone[] = [
    { label: 'Blocked', tickets: board?.blocked ?? [], emphasis: 'loud' },
    { label: 'Working', tickets: board?.working ?? [] },
  ];
  const doneZones: BoardColumnZone[] = [{ label: 'Done', tickets: board?.done ?? [] }];

  return (
    <section aria-label="Board" data-role="board" data-connection-state={connectionState}>
      <div data-role="board-toolbar">
        <CapacityChip workerFree={board?.worker_free ?? 0} workerTotal={board?.worker_total ?? 0} />
        <span data-role="connection-chip" data-connection-state={connectionState}>
          {connectionStateLabel(connectionState)}
        </span>
      </div>
      <div data-role="board-columns">
        <BoardColumn title="Backlog" zones={backlogZones} />
        <BoardColumn title="Developing" zones={developingZones} />
        <BoardColumn title="Done" zones={doneZones} />
      </div>
    </section>
  );
}

/** Visible connected/reconnecting indicator (07 §8) alongside the capacity chip. */
function connectionStateLabel(state: ConnectionState): string {
  switch (state) {
    case 'connected':
      return 'Connected';
    case 'reconnecting':
      return 'Reconnecting…';
    case 'connecting':
      return 'Connecting…';
  }
}
