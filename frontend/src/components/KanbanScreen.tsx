// `/kanban` — the board view (route wiring seam).
//
// Same shape as `PrimaryScreen`: this file is only the seam (stores → props,
// intent → transport) and every pixel lives in the presentational
// `KanbanScreenView`. It is a SECOND desktop shell over the same board store,
// not a variant of the first — `/app` answers "what should I look at now", this
// answers "where does everything stand", and the two are different enough
// questions that folding them into one screen with a toggle would compromise
// both.
//
// **A thinner provider stack than `/app`'s, on purpose.** A board view reads the
// board; it does not render the feed, so `FeedProvider` is not mounted and this
// route costs no `GET /api/feed`. `ActivityProvider` is here because the voice
// store depends on it, and the voice store is here because the ticket sheet this
// view opens is the same sheet the feed opens, down to being able to talk to a
// ticket from it.
//
// The action callbacks are `PrimaryScreen`'s, minus the feed's optimistic card
// hides (there are no cards to hide — the board snapshot is the only thing on
// screen, and it comes back over the stream). What they express is identical:
// accept/delete/poke route through the brain (D5), the sandbox option, the two
// sandbox overrides and the text edit are direct writes (see TicketDetail).
import { useCallback, type JSX } from 'react';
import { BoardProvider } from '@/stores/board-store';
import { ActivityProvider } from '@/stores/activity-store';
import { VoiceProvider } from '@/voice/voice-store';
import { useBoardStore } from '@/stores/board-context';
import { useCurrentProject } from '@/stores/current-project-context';
import { useNotificationMode } from '@/stores/use-notification-mode';
import { useWebPush } from '@/stores/use-web-push';
import { usePresence } from '@/stores/use-presence';
import { useProjectsStatus } from '@/stores/use-projects-status';
import {
  acceptTicket,
  deleteTicket,
  editTicketText,
  killTicketSandbox,
  postMessage,
  reassignTicketSandbox,
  setTicketSandbox,
} from '@/transport/transport';
import type { TicketTextEdit } from '@/components/TicketDetail';
import { KanbanScreenView } from '@/components/desktop/KanbanScreenView';
import type { RailProject } from '@/components/desktop/ProjectsRail';

function KanbanScreenBody(): JSX.Element {
  // Report foreground presence so the backend withholds a duplicate Web Push
  // while this tab is visible (02 §10 push dedup) — a board left open all day is
  // exactly the tab that would otherwise get pushed at.
  usePresence();

  const { board, connectionState, loading, refreshBoard } = useBoardStore();
  const { current, projects, selectProject } = useCurrentProject();
  const { mode: notificationMode, setMode: setNotificationMode } = useNotificationMode();
  const { status: pushStatus, enable: enablePush, disable: disablePush } = useWebPush();

  // The rail's ambient layer, exactly as `/app` builds it: the selected
  // project's state comes off the live board for free, the others are polled.
  // The rail is always up on this route (there is no mobile shell here), so the
  // poll is always on — same cost as the desktop feed shell.
  const projectStates = useProjectsStatus(projects, current?.id ?? null, board);
  const railProjects: RailProject[] = projects.map((project) => {
    const status = projectStates[project.id];
    return {
      id: project.id,
      name: project.name,
      state: status?.state ?? 'unknown',
      working: status?.working ?? 0,
    };
  });

  const onAccept = useCallback((ticketId: string): void => {
    // Straight to the accept endpoint (08 §5 / D6); the resulting board
    // transition comes back over the stream and re-columns the card itself.
    void acceptTicket(ticketId);
  }, []);

  const onDelete = useCallback((ticketId: string): void => {
    // Routed through the brain (delete_ticket, D5). A blocked delete also
    // releases the ticket's worker board-side. Fire-and-forget: the board's
    // next snapshot is the confirmation.
    void deleteTicket(ticketId);
  }, []);

  const onPoke = useCallback((ticketId: string): void => {
    // The client can't (and by D5 mustn't) command an agent directly —
    // send_to_agent is a brain tool — so a poke is expressed as a human message
    // naming the ticket and the brain resolves it.
    void postMessage(`Poke the agent on ticket ${ticketId} to continue.`);
  }, []);

  const onSetKeepSandbox = useCallback(
    (ticketId: string, keep: boolean): void => {
      // A setting on the ticket rather than a board transition, so it writes
      // directly and the board.updated it emits brings the value back. Refresh
      // on failure so the sheet snaps back to the truth at once rather than on
      // its optimistic time-box.
      void setTicketSandbox(ticketId, keep).catch(() => {
        refreshBoard();
      });
    },
    [refreshBoard],
  );

  const onKillSandbox = useCallback(
    (ticketId: string): void => {
      // A manual override for a wedged sandbox — direct, because the whole
      // reason it exists is that waiting for the orchestrator to notice is the
      // problem.
      void killTicketSandbox(ticketId).catch(() => {
        refreshBoard();
      });
    },
    [refreshBoard],
  );

  const onReassignSandbox = useCallback(
    (ticketId: string): void => {
      void reassignTicketSandbox(ticketId).catch(() => {
        refreshBoard();
      });
    },
    [refreshBoard],
  );

  const onEditText = useCallback(
    (ticketId: string, patch: TicketTextEdit): void => {
      // The one place the user's own words must land on the board untouched, so
      // it skips the brain: routing a correction through an LLM pass is exactly
      // the drift the affordance exists to remove.
      void editTicketText(ticketId, patch).catch(() => {
        refreshBoard();
      });
    },
    [refreshBoard],
  );

  return (
    <KanbanScreenView
      projects={railProjects}
      currentProjectId={current?.id ?? null}
      onSelectProject={selectProject}
      board={board}
      connectionState={connectionState}
      loading={loading}
      onAccept={onAccept}
      onDelete={onDelete}
      onPoke={onPoke}
      onSetKeepSandbox={onSetKeepSandbox}
      onKillSandbox={onKillSandbox}
      onReassignSandbox={onReassignSandbox}
      onEditText={onEditText}
      notificationMode={notificationMode}
      onSelectNotificationMode={setNotificationMode}
      pushStatus={pushStatus}
      onEnablePush={() => {
        void enablePush();
      }}
      onDisablePush={() => {
        void disablePush();
      }}
    />
  );
}

export function KanbanScreen(): JSX.Element {
  return (
    <BoardProvider>
      <ActivityProvider>
        <VoiceProvider>
          <KanbanScreenBody />
        </VoiceProvider>
      </ActivityProvider>
    </BoardProvider>
  );
}
