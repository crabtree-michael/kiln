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
import { type JSX } from 'react';
import { BoardProvider } from '@/stores/board-store';
import { ActivityProvider } from '@/stores/activity-store';
import { VoiceProvider } from '@/voice/voice-store';
import { useBoardStore } from '@/stores/board-context';
import { useCurrentProject } from '@/stores/current-project-context';
import { useNotificationMode } from '@/stores/use-notification-mode';
import { useWebPush } from '@/stores/use-web-push';
import { usePresence } from '@/stores/use-presence';
import { useProjectsStatus } from '@/stores/use-projects-status';
import { useTicketActions } from '@/components/ticket-intents';
import { useKeyboardViewport } from '@/components/use-keyboard-viewport';
import { KanbanScreenView } from '@/components/desktop/KanbanScreenView';
import type { RailProject } from '@/components/desktop/ProjectsRail';

function KanbanScreenBody(): JSX.Element {
  // Publish the soft keyboard's overlap, exactly as `/app` does. This shell is a
  // 100dvh column over a locked document too, and it opens the same ticket sheet
  // — whose body editor is an editable field the keyboard would otherwise cover,
  // since the keyboard overlays rather than resizing (index.html). Inert wherever
  // no soft keyboard shows, which is every mouse-driven window.
  useKeyboardViewport();

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

  // The same seven ticket intents `/app` wires, from the same hook — see
  // `ticket-intents.ts`.
  //
  // NO optimistic card hides are passed, and that omission is the point rather
  // than an oversight: this route mounts no `FeedProvider` (see the header —
  // a board view costs no `GET /api/feed`), so there is no feed card to hide.
  // Accepting here just re-columns the ticket, which the next board snapshot
  // does on its own. Wiring a hide in would require reintroducing the feed to
  // this route; leaving it out is what keeps that decision visible here instead
  // of buried in shared code (plan §7).
  const {
    onAccept,
    onDelete,
    onPoke,
    onSetKeepSandbox,
    onKillSandbox,
    onReassignSandbox,
    onEditText,
  } = useTicketActions({ refreshBoard });

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
