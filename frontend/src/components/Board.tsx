// Composes the board region (07 §7): three columns in order — Backlog
// (Shaping over Ready), Developing (Blocked above Working), Done — plus the
// capacity chip. Board is read-only: no drag-and-drop, all mutation flows
// through chat (D5). Reads from the board store; `connectionState` also
// drives the "dim while reconnecting" treatment (07 §8).
//
// Debug view: clicking a card opens a read-only detail overlay for that
// ticket. Selection is local view state only — it opens/closes an overlay and
// never mutates the board (D5 still holds: the board stays drag-free and every
// state change flows through the brain).
import { useState, type JSX } from 'react';
import { useBoardStore } from '@/stores/board-context';
import type { ConnectionState } from '@/transport/transport';
import { BoardColumn, type BoardColumnZone } from '@/components/BoardColumn';
import { CapacityChip } from '@/components/CapacityChip';
import { TicketDetail } from '@/components/TicketDetail';
import type { Ticket } from '@/components/TicketCard';

export function Board(): JSX.Element {
  const { board, connectionState } = useBoardStore();
  // Which ticket's detail overlay is open (null = none).
  const [selectedTicket, setSelectedTicket] = useState<Ticket | null>(null);

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
        <BoardColumn title="Backlog" zones={backlogZones} onSelectTicket={setSelectedTicket} />
        <BoardColumn
          title="Developing"
          zones={developingZones}
          onSelectTicket={setSelectedTicket}
        />
        <BoardColumn title="Done" zones={doneZones} onSelectTicket={setSelectedTicket} />
      </div>
      {selectedTicket !== null && (
        <TicketDetail
          ticket={selectedTicket}
          onClose={() => {
            setSelectedTicket(null);
          }}
        />
      )}
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
