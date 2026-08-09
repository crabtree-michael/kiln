// Primary screen (08 §6). Composes the feed + activity providers and bridges
// their stores into a presentational view. All markup, CSS, and the 08 §F
// selector surface live in the presentational tree; this file is only the wiring
// seam (stores → props, Accept → transport).
//
// There are now TWO presentational views over this one seam, and picking between
// them is the last thing this file does: `PrimaryScreenView` (the mobile screen,
// 08) and `DesktopScreenView` (the desktop experience, 13). That is 13 §13 Q4's
// answer — one responsive tree, two shells over shared stores — and it is why
// every store read, every optimistic hide and every transport call above the
// switch is written once. The shells differ in shape, not in truth: a desktop
// Accept is the same `MarkReady` a mobile one is (13 §7).
import { type JSX } from 'react';
import { BoardProvider } from '@/stores/board-store';
import { FeedProvider } from '@/stores/feed-store';
import { ActivityProvider } from '@/stores/activity-store';
import { VoiceProvider } from '@/voice/voice-store';
import { useBoardStore } from '@/stores/board-context';
import { useFeedStore } from '@/stores/feed-context';
import { useActivityStore } from '@/stores/activity-context';
import { useNotificationMode } from '@/stores/use-notification-mode';
import { useWebPush } from '@/stores/use-web-push';
import { usePresence } from '@/stores/use-presence';
import { type MeProject } from '@/transport/transport';
import { useTicketActions } from '@/components/ticket-intents';
import { PrimaryScreenView } from '@/components/PrimaryScreenView';
import { ProjectSwitcher } from '@/components/ProjectSwitcher';
import { useKeyboardViewport } from '@/components/use-keyboard-viewport';
import { useCurrentProject } from '@/stores/current-project-context';
import { useProjectsStatus } from '@/stores/use-projects-status';
import { DesktopScreenView } from '@/components/desktop/DesktopScreenView';
import { DesktopComposer } from '@/components/desktop/DesktopComposer';
import type { RailProject } from '@/components/desktop/ProjectsRail';
import { useIsDesktop } from '@/components/desktop/use-desktop-layout';

/** Stable empty list handed to `useProjectsStatus` while the mobile shell is up.
 * The rail is the only consumer of cross-project state, and the mobile screen has
 * no rail — so on a phone the hook must cost nothing, not one GET per project per
 * interval for a surface that isn't rendered. A module constant rather than a
 * literal so the identity never churns the hook's effect. */
const NO_RAIL_PROJECTS: MeProject[] = [];

function PrimaryScreenBody(): JSX.Element {
  // Keep the screen column matched to the visible viewport while the keyboard is
  // open, so the dock stays docked above it rather than sliding off-screen.
  useKeyboardViewport();

  // Report foreground presence so the backend withholds a duplicate Web Push
  // while this tab is visible (02 §10 push dedup). No-ops until notifications
  // are enabled (no subscription ⇒ nothing to suppress).
  usePresence();

  const {
    feed,
    connectionState,
    loading: feedLoading,
    lastSeenId,
    hasEarlier,
    loadingEarlier,
    showEarlier,
    refreshFeed,
    acceptProposal,
    deleteTicketCard,
    dismissCard,
    dismissAll,
  } = useFeedStore();
  const { board, refreshBoard, refreshing, loading: boardLoading } = useBoardStore();
  // One answer to "is this project still loading?" for the shell below. Both
  // stores are seeded from the per-project cache on a switch and then refresh
  // (12 §4.1), so either one still being in flight means what's on screen is the
  // last thing we knew rather than the current thing — which is exactly what the
  // indication is for. OR rather than AND: the strip should be up for the whole
  // wait, not just the part where both halves happen to overlap.
  const loading = boardLoading || feedLoading;
  const { thinking, toasts, dismiss, setToastExpanded } = useActivityStore();
  const { mode: notificationMode, setMode: setNotificationMode } = useNotificationMode();
  const { status: pushStatus, enable: enablePush, disable: disablePush } = useWebPush();

  // Which shell to wear (13 D8/§13 Q4). Read unconditionally, like every hook
  // below it — the switch happens at the end of this function, not around it.
  const isDesktop = useIsDesktop();
  const { current, projects, selectProject } = useCurrentProject();
  // The rail's ambient layer (13 §5): the selected project's state comes off the
  // live board for free; the others are polled. Ordering is the session's own,
  // passed through untouched — a rail that re-sorts itself by urgency would move
  // rows under the pointer exactly when they matter.
  const projectStates = useProjectsStatus(
    isDesktop ? projects : NO_RAIL_PROJECTS,
    current?.id ?? null,
    board,
  );
  const railProjects: RailProject[] = projects.map((project) => {
    const status = projectStates[project.id];
    return {
      id: project.id,
      name: project.name,
      state: status?.state ?? 'unknown',
      // Carried alongside the state because the state's precedence drops it: a
      // project that is both blocked and building reads as `needs-you`. The rail
      // spends it on hover/assistive text only — never a badge (13 §8).
      working: status?.working ?? 0,
    };
  });

  // The seven ticket intents, shared verbatim with `/kanban` (see
  // `ticket-intents.ts` for why each one does or does not go through the brain).
  // This screen HAS a feed, so it hands the hook the feed store's two optimistic
  // card hides — the one thing that differs between the two containers, injected
  // rather than looked up (plan §7).
  const {
    onAccept,
    onDelete,
    onPoke,
    onSetKeepSandbox,
    onKillSandbox,
    onReassignSandbox,
    onEditText,
  } = useTicketActions({
    refreshBoard,
    onAcceptOptimistic: acceptProposal,
    onDeleteOptimistic: deleteTicketCard,
  });

  // The switch. Everything above is shared; only the shape below differs.
  //
  // Four things the desktop view deliberately does NOT receive, each for a
  // reason in the doc rather than as an oversight:
  //   - `onDismissCard` — the mobile swipe has no desktop equivalent and does not
  //     become a hover-revealed close button by default (13 §6). Whether any card
  //     is dismissible by hand on a desk is a genuinely open question (§13 Q3),
  //     and the curated-feed model (08 D1) argues the brain should be removing
  //     things, not the user.
  //   - `onDismissAll` — same call, and a bulk clear is a management affordance in
  //     a window whose entire premise is that there is nothing to manage.
  //   - `onRefreshFeed` — pull-to-refresh is a touch gesture; the desk equivalent
  //     is that the stream is live and the feed just changes (13 §8.3).
  //   - `onOpenTickets`/`ticketsRefreshing`/`brand` — the mobile header's ticket
  //     dropdown and wordmark-switcher are what the rail replaces outright (D3).
  if (isDesktop) {
    return (
      <DesktopScreenView
        projects={railProjects}
        currentProjectId={current?.id ?? null}
        onSelectProject={selectProject}
        feed={feed}
        board={board}
        connectionState={connectionState}
        loading={loading}
        thinking={thinking}
        toasts={toasts}
        onDismiss={dismiss}
        onToastExpandedChange={setToastExpanded}
        onAccept={onAccept}
        onDelete={onDelete}
        onPoke={onPoke}
        onSetKeepSandbox={onSetKeepSandbox}
        onKillSandbox={onKillSandbox}
        onReassignSandbox={onReassignSandbox}
        onEditText={onEditText}
        lastSeenId={lastSeenId}
        hasEarlier={hasEarlier}
        loadingEarlier={loadingEarlier}
        onShowEarlier={showEarlier}
        notificationMode={notificationMode}
        onSelectNotificationMode={setNotificationMode}
        pushStatus={pushStatus}
        onEnablePush={() => {
          void enablePush();
        }}
        onDisablePush={() => {
          void disablePush();
        }}
        composer={<DesktopComposer />}
      />
    );
  }

  // And one thing the MOBILE view no longer receives: the notification settings.
  // The phone's top bar dropped its bell (too many icons in one bar), so the
  // frequency + push toggle are reached from the settings page — which the
  // project switcher's own "Settings" item now opens. The desk keeps its bell:
  // the rail's foot has the room the phone's header didn't. The hooks above stay
  // unconditional, as every hook here is; only the shape below the switch differs.
  return (
    <PrimaryScreenView
      brand={<ProjectSwitcher />}
      feed={feed}
      board={board}
      connectionState={connectionState}
      thinking={thinking}
      toasts={toasts}
      onDismiss={dismiss}
      onToastExpandedChange={setToastExpanded}
      onAccept={onAccept}
      onDelete={onDelete}
      onPoke={onPoke}
      onSetKeepSandbox={onSetKeepSandbox}
      onKillSandbox={onKillSandbox}
      onReassignSandbox={onReassignSandbox}
      onEditText={onEditText}
      onDismissCard={dismissCard}
      onDismissAll={dismissAll}
      onOpenTickets={refreshBoard}
      ticketsRefreshing={refreshing}
      lastSeenId={lastSeenId}
      hasEarlier={hasEarlier}
      loadingEarlier={loadingEarlier}
      onShowEarlier={showEarlier}
      onRefreshFeed={refreshFeed}
    />
  );
}

export function PrimaryScreen(): JSX.Element {
  return (
    <BoardProvider>
      <FeedProvider>
        <ActivityProvider>
          <VoiceProvider>
            <PrimaryScreenBody />
          </VoiceProvider>
        </ActivityProvider>
      </FeedProvider>
    </BoardProvider>
  );
}
