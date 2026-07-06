// Composes the board region (07 §7): two columns — Backlog (Ready) and
// Developing (Blocked above Working) — plus the capacity chip. Shaping and
// Done are hidden from the list; the active work states (Ready, Blocked,
// Working) stay visible. Board is read-only: no drag-and-drop, all mutation
// flows through chat (D5). Reads from the board store; `connectionState` also
// drives the "dim while reconnecting" treatment (07 §8), left to the solution
// phase's styling.
import { useState, type JSX } from 'react';
import { useBoardStore } from '@/stores/board-context';
import type { ConnectionState } from '@/transport/transport';
import { BoardColumn, type BoardColumnZone } from '@/components/BoardColumn';
import { CapacityChip } from '@/components/CapacityChip';
import { TicketDetail } from '@/components/TicketDetail';
import type { Ticket } from '@/components/TicketCard';

export function Board(): JSX.Element {
  const { board, connectionState } = useBoardStore();
  // Which ticket's read-only detail is open (07 §7). Local view state only;
  // the client holds no authoritative state (02 §11).
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const backlogZones: BoardColumnZone[] = [{ label: 'Ready', tickets: board?.ready ?? [] }];
  const developingZones: BoardColumnZone[] = [
    { label: 'Blocked', tickets: board?.blocked ?? [], emphasis: 'loud' },
    { label: 'Working', tickets: board?.working ?? [] },
  ];

  // Re-derive the open ticket from the live board each render so the detail
  // reflects incoming `board` snapshots; if it left the board, close.
  const selectedTicket =
    selectedId != null
      ? (allTickets(board).find((ticket) => ticket.id === selectedId) ?? null)
      : null;
  const onSelectTicket = (ticket: Ticket): void => {
    setSelectedId(ticket.id);
  };

  return (
    <section aria-label="Board" data-role="board" data-connection-state={connectionState}>
      <div data-role="board-toolbar">
        <CapacityChip workerFree={board?.worker_free ?? 0} workerTotal={board?.worker_total ?? 0} />
        <span data-role="connection-chip" data-connection-state={connectionState}>
          {connectionStateLabel(connectionState)}
        </span>
      </div>
      <div data-role="board-columns">
        <BoardColumn title="Backlog" zones={backlogZones} onSelectTicket={onSelectTicket} />
        <BoardColumn title="Developing" zones={developingZones} onSelectTicket={onSelectTicket} />
      </div>
      {selectedTicket != null && (
        <TicketDetail
          ticket={selectedTicket}
          showInternalMeta
          onClose={() => {
            setSelectedId(null);
          }}
        />
      )}
    </section>
  );
}

/** All tickets across every zone, in column order. */
function allTickets(board: ReturnType<typeof useBoardStore>['board']): Ticket[] {
  if (board == null) return [];
  return [...board.shaping, ...board.ready, ...board.blocked, ...board.working, ...board.done];
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
