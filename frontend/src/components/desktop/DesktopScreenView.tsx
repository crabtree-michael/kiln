// The desktop shell, presentational (13 §3–§10). Pure props in → the whole
// two-region layout out, so tests render it directly with fixture data and never
// touch the live stores. `PrimaryScreen` bridges the same stores that feed the
// mobile view into these props.
//
// Three regions, left to right (13 §3, amended): the projects rail, the
// in-progress panel, and the selected project's feed with the input under it.
// The middle column is the one addition to the original two-region shape, and it
// is deliberately NOT an inspector: it holds no selection, shows no detail, and
// answers exactly one standing question — what is being worked on right now. It
// is separated from the feed by a rule rather than by space, because unlike the
// rail (peripheral furniture, set apart by its recessed surface) it is content
// about the same project the feed is about, and the line is what says "these are
// two readings of one thing" instead of "this is more feed".
//
// There is still no board and no detail pane — ticket detail is an OVERLAY,
// opened against the window's right edge so the work stays in sight (13 D7/D7a)
// but taking no width from any column when it is closed — and raw state still
// lives at `/debug` (08 §6).
//
// The register is 13 §1: present without being loud. Everything here that looks
// like restraint is load-bearing — the accent appears only on `needs-you`, the
// only thing allowed to animate on its own is the working indication, and
// arrivals fade rather than slide. Spending any of that elsewhere is what breaks
// the one loud moment when it comes.
import { useCallback, useRef, useState, type JSX, type KeyboardEvent } from 'react';
import type {
  Board,
  ConnectionState,
  FeedCard,
  FeedSnapshot,
  FeedSummary,
  NotificationModeValue,
} from '@/transport/transport';
import type { ActivityToast } from '@/stores/activity-context';
import type { WebPushStatus } from '@/stores/use-web-push';
import type { Ticket } from '@/components/TicketCard';
import { FeedCardItem } from '@/components/FeedCardItem';
import { TicketDetail, type TicketTextEdit } from '@/components/TicketDetail';
import { TicketDetailTranscript } from '@/components/TicketDetailTranscript';
import { TicketDetailVoiceActions } from '@/components/TicketDetailVoiceActions';
import { ActivityRow } from '@/components/ActivityRow';
import { DesktopRail } from '@/components/desktop/DesktopRail';
import type { RailProject } from '@/components/desktop/ProjectsRail';
import { WorkingNow } from '@/components/desktop/WorkingNow';
import { blockedCount, workingTickets } from '@/components/desktop/working-now';
import { useDesktopShellFlag } from '@/components/desktop/use-desktop-layout';
import { useDeepLinkTicket } from '@/components/use-deep-link-ticket';
import { lastWordDetail, streamDetail } from '@/components/feed-format';
import '@/components/PrimaryScreen.css';
import '@/components/desktop/DesktopScreen.css';

const EMPTY_SUMMARY: FeedSummary = {
  blocker_count: 0,
  update_count: 0,
  stream_count: 0,
  building: 0,
  idle: 0,
};

export interface DesktopScreenViewProps {
  /** The rail's rows, in a stable order with each one's ambient state (13 §5). */
  projects: RailProject[];
  currentProjectId: string | null;
  onSelectProject: (id: string) => void;
  feed: FeedSnapshot | null;
  board?: Board | null;
  connectionState: ConnectionState;
  /** True while this project's state is being fetched — the project-switch wait
   * (12 §4.1), including the refresh that runs behind a cache-restored feed.
   * Drives the one line that says so; see the render below for why the resting
   * state is withheld while it is up. */
  loading?: boolean;
  thinking: boolean;
  toasts: ActivityToast[];
  onDismiss: (id: number) => void;
  onToastExpandedChange?: ((id: number, expanded: boolean) => void) | undefined;
  onAccept: (ticketId: string) => void;
  onDelete?: ((ticketId: string) => void) | undefined;
  onPoke?: ((ticketId: string) => void) | undefined;
  onSetKeepSandbox?: ((ticketId: string, keep: boolean) => void) | undefined;
  onKillSandbox?: ((ticketId: string) => void) | undefined;
  onReassignSandbox?: ((ticketId: string) => void) | undefined;
  onEditText?: ((ticketId: string, patch: TicketTextEdit) => void) | undefined;
  lastSeenId?: number | null;
  /** Anything further back to show — collapsed already-seen cards, or older
   * history on the server (08 D2‴). The desk gets the same one control as the
   * phone: the feed collapses what has been seen everywhere it is rendered. */
  hasEarlier?: boolean;
  loadingEarlier?: boolean;
  onShowEarlier?: (() => void) | undefined;
  notificationMode?: NotificationModeValue;
  onSelectNotificationMode?: ((mode: NotificationModeValue) => void) | undefined;
  pushStatus?: WebPushStatus | undefined;
  onEnablePush?: (() => void) | undefined;
  onDisablePush?: (() => void) | undefined;
  /** The input under the feed (13 §7). A slot rather than a direct render so the
   * presentational tests can mount this shell without a `VoiceProvider`; the
   * composing screen passes the real `DesktopComposer`. Mirrors how the mobile
   * view takes its `brand` slot. */
  composer?: JSX.Element | undefined;
  /** Injected "now" for deterministic relative-age rendering. */
  now?: number;
}

/** An update/preview card's numeric notification_id, or null for board cards —
 * the last-seen divider boundary (08 D2′). Identical rule to the mobile view;
 * the divider means the same thing on a desk, it just gets scrolled past more. */
function updateId(card: FeedCard): number | null {
  const isUpdate = card.kind === 'update' || card.kind === 'preview';
  return isUpdate && typeof card.notification_id === 'number' ? card.notification_id : null;
}

/** Index of the first card at/below the last-seen boundary, or -1 when there is
 * no divider to draw (08 D2′). */
function dividerIndex(cards: FeedCard[], lastSeenId: number | null): number {
  if (lastSeenId === null) {
    return -1;
  }
  const firstOld = cards.findIndex((card) => {
    const id = updateId(card);
    return id !== null && id <= lastSeenId;
  });
  if (firstOld === -1) {
    return -1;
  }
  const hasNewerAbove = cards.slice(0, firstOld).some((card) => {
    const id = updateId(card);
    return id !== null && id > lastSeenId;
  });
  return hasNewerAbove ? firstOld : -1;
}

/** Whether a card is already-seen history, rendered de-emphasized (08 D2′). */
function isSeen(card: FeedCard, lastSeenId: number | null): boolean {
  if (lastSeenId === null) {
    return false;
  }
  const id = updateId(card);
  return id !== null && id <= lastSeenId;
}

/** The full ticket a card points at, resolved against the live board snapshot.
 * Every bucket is scanned so a ticket that moves state between the click and the
 * render still resolves. */
function findTicket(board: Board | null, id: string | null): Ticket | null {
  if (board === null || id === null) {
    return null;
  }
  const all: Ticket[] = [
    ...board.shaping,
    ...board.ready,
    ...board.blocked,
    ...board.working,
    ...board.done,
  ];
  return all.find((ticket) => ticket.id === id) ?? null;
}

export function DesktopScreenView({
  projects,
  currentProjectId,
  onSelectProject,
  feed,
  board = null,
  connectionState,
  loading = false,
  thinking,
  toasts,
  onDismiss,
  onToastExpandedChange,
  onAccept,
  onDelete,
  onPoke,
  onSetKeepSandbox,
  onKillSandbox,
  onReassignSandbox,
  onEditText,
  lastSeenId = null,
  hasEarlier = false,
  loadingEarlier = false,
  onShowEarlier,
  notificationMode = 'blocked',
  onSelectNotificationMode,
  pushStatus,
  onEnablePush,
  onDisablePush,
  composer,
  now = Date.now(),
}: DesktopScreenViewProps): JSX.Element {
  const summary = feed?.summary ?? EMPTY_SUMMARY;
  const cards = feed?.cards ?? [];
  // The resting-state subtext, null when the brain has never spoken.
  const lastWord = lastWordDetail(summary, now);
  const divider = dividerIndex(cards, lastSeenId);
  // "Working" for the *selected* project: the brain mid-pass, or agents mid-turn
  // (13 §8.2). Drives the breathing indication — the one thing on this screen
  // permitted to animate on its own. Never a progress bar: there is no progress
  // to report, and a bar that doesn't measure anything is a lie.
  const working = thinking || summary.building > 0;
  // …and WHICH tickets those agents are on, straight off the board's Working
  // bucket. The liveness signal above and this list are kept separate on purpose:
  // the brain thinks with nothing in Working, and a board snapshot can name a
  // working ticket before the feed summary agrees. Either lights the strip.
  const inProgress = workingTickets(board);
  // Disconnected must be STATED, not hidden (13 §10): an ambient app that has
  // silently stopped receiving is worse than one that is visibly off. Low-key
  // and permanent while it lasts — never a modal, and deliberately not in the
  // accent, which is reserved for things needing a decision.
  const disconnected = connectionState === 'reconnecting';

  const [openTicketId, setOpenTicketId] = useState<string | null>(null);
  const closeTicket = useCallback((): void => {
    setOpenTicketId(null);
  }, []);
  // A tapped push notification deep-links here exactly as it does on mobile
  // (02 §10 / 12 §6.3) — desktop being open changes nothing about being found.
  useDeepLinkTicket(setOpenTicketId);
  // A live voice session inside the open panel, reported up from its voice cluster
  // — the same seam and the same reason as the mobile shell's (this view is not a
  // voice consumer either). The rearrangement it drives is the sheet's own, so both
  // shells get it from one place: the mic joins Send and × at the panel's trailing
  // end and Accept stands down while the user is speaking.
  const [ticketVoiceActive, setTicketVoiceActive] = useState(false);
  const openTicket = findTicket(board, openTicketId);
  // The open ticket's bound session, for the panel's gear menu status line only —
  // Poke is no longer gated on it reading `idle` (see TicketDetail's onPoke).
  const openAgentStatus =
    openTicket === null
      ? undefined
      : board?.agents.find((agent) => agent.ticket_id === openTicket.id)?.status;

  const feedRef = useRef<HTMLElement>(null);

  // Moving between cards in the feed with the keyboard (13 §9). The feed region
  // itself is the tab stop; from there Arrow keys move a roving focus through the
  // rows (each `tabIndex={-1}`, so they never bloat the Tab order). This is the
  // half of "a full pass without the mouse" that the rail's own arrow handling
  // doesn't cover.
  const onFeedKeyDown = (event: KeyboardEvent<HTMLElement>): void => {
    const keys = ['ArrowDown', 'ArrowUp', 'Home', 'End'];
    if (!keys.includes(event.key)) {
      return;
    }
    const region = feedRef.current;
    if (region === null) {
      return;
    }
    const rows = Array.from(region.querySelectorAll<HTMLElement>('[data-role="desktop-feed-row"]'));
    if (rows.length === 0) {
      return;
    }
    // Which row focus is currently inside — `contains`, not identity, so arrowing
    // still works from a focused control *within* a card (an Accept button, an
    // expanded body) rather than dead-ending there.
    const active = rows.findIndex((row) => row.contains(document.activeElement));
    let next: number;
    if (event.key === 'Home') {
      next = 0;
    } else if (event.key === 'End') {
      next = rows.length - 1;
    } else if (event.key === 'ArrowDown') {
      next = active === -1 ? 0 : Math.min(active + 1, rows.length - 1);
    } else {
      next = active === -1 ? rows.length - 1 : Math.max(active - 1, 0);
    }
    event.preventDefault();
    rows[next]?.focus();
  };

  // This shell pins NO theme. It used to stamp `data-theme="dark"` on <body>
  // (13 D6, "dark is desktop's resting register"), which won over the system
  // preference `ThemeColorSync` writes to <html> and left a light-mode user
  // staring into a dark window on a desk and a paper one on their phone. The
  // preference belongs to the person, not to the viewport width, so desktop
  // follows the OS the way every other route already does — one mechanism,
  // `data-theme` on <html>, live-updated on `prefers-color-scheme` flips.
  //
  // What survives D6 is the part that was actually about design rather than
  // about defaults: the dark register is still Kiln's existing WARM near-black,
  // and this file's CSS reads only semantic tokens, so both themes come from
  // tokens.css and neither is a desktop-only palette fork.
  //
  // `data-shell="desktop"` is still stamped, and for a reason unrelated to
  // theming: it is how the ticket sheet — which portals out of this subtree —
  // finds out it is a side panel rather than a bottom sheet. It moved into a
  // hook once `/kanban` became a second desktop shell that needs exactly the
  // same stamp; see `useDesktopShellFlag` for the full reasoning.
  useDesktopShellFlag();

  return (
    <div data-role="desktop-screen" data-connection-state={connectionState}>
      {/* The rail, shared verbatim with the `/kanban` board view — one
          component, not two that agree today. See DesktopRail. */}
      <DesktopRail
        projects={projects}
        currentProjectId={currentProjectId}
        onSelectProject={onSelectProject}
        disconnected={disconnected}
        notificationMode={notificationMode}
        onSelectNotificationMode={onSelectNotificationMode}
        pushStatus={pushStatus}
        onEnablePush={onEnablePush}
        onDisablePush={onDisablePush}
      />

      {/* The working indication (13 §8.2), and what it is working ON — its own
          column, beside the feed rather than above it. A property of the project
          that stays true for as long as the work runs has no business inside (or
          on top of) a scrolling history: here it cannot be scrolled away, and it
          keeps its own reading rhythm instead of borrowing the feed's measure
          and reading as the first card.

          Scoped to the SELECTED project, like the feed beside it. The rail's
          per-project working counts come from a slow cross-project poll
          (`useProjectsStatus`, minutes stale by design) and naming tickets from
          it would put stale titles on screen next to a live feed; the board
          store behind this column is the live one.

          The column is always here, empty or not — see WorkingNow for why the
          geometry holds still. Breathing, slow and low-contrast; the per-ticket
          marks are the phone's, unchanged. See DesktopScreen.css. */}
      <div data-role="desktop-working-panel">
        <WorkingNow
          tickets={inProgress}
          blocked={blockedCount(board)}
          active={working}
          onOpenTicket={setOpenTicketId}
          now={now}
        />
      </div>

      <main data-role="desktop-main">
        {/* The project-switch wait, stated (12 §4.1). Switching used to give no
            sign at all: the rail's selection moved and the feed sat on whatever
            it had — nothing, on a first visit — until a round-trip landed, which
            reads as a window that has stopped working. So it says so, in flow
            above the feed's scroller rather than as a card in it: like the
            in-progress column beside it, this is a fact about the whole project
            rather than about any one card.

            The register is 13 §1, not a progress bar and not the accent: one
            faint line and the smallest possible turning mark. It is honest about
            being indeterminate — we do not know how long a fetch takes — and it
            goes away the moment the snapshot lands. */}
        {loading && (
          <div data-role="desktop-loading">
            {/* Outer holds the gutter, inner holds the reading column — the
                working strip's split, so the two line up exactly. */}
            <div data-role="desktop-loading-line" role="status">
              <span data-role="desktop-loading-mark" aria-hidden="true" />
              Loading…
            </div>
          </div>
        )}
        <section
          ref={feedRef}
          role="region"
          aria-label="Feed"
          data-role="desktop-feed"
          tabIndex={0}
          onKeyDown={onFeedKeyDown}
        >
          {cards.length === 0 ? (
            // The resting state is the real state (13 §1): composed, not empty,
            // and not apologised for. The bell mark over one honest line, no
            // "nothing here yet!" — this is the state the design is optimised
            // for, so it should look like the app at rest. It is the same mark,
            // in the same reading, as the phone's all-clear state: the two
            // shells are one app, and the resting view is where that matters
            // most, since it is what a window left open all day shows.
            //
            // Withheld while `loading`, because it is a STATEMENT: "All quiet"
            // asserts we asked and there was nothing, and mid-fetch we haven't
            // asked yet. Saying it and then replacing it a beat later with three
            // blockers is worse than saying nothing — it teaches the user not to
            // believe the one line this screen most needs them to believe. The
            // loading line above stands in until the answer is actually known.
            !loading && (
              <div data-role="desktop-rest">
                <img data-role="desktop-rest-mark" src="/kiln-mark.svg" alt="" aria-hidden="true" />
                <p data-role="desktop-rest-line">All quiet.</p>
                <p data-role="desktop-rest-detail">{streamDetail(summary)}</p>
                {lastWord !== null && <p data-role="desktop-rest-subtext">{lastWord}</p>}
              </div>
            )
          ) : (
            <ol data-role="desktop-feed-list">
              {cards.map((card, index) => (
                <li key={card.id}>
                  {index === divider && (
                    <div data-role="feed-divider" data-variant="last-seen">
                      Earlier
                    </div>
                  )}
                  {/* The roving-focus target. `tabIndex={-1}` keeps it out of the
                      Tab order (Tab still walks the card's own controls); the
                      feed region's Arrow handling focuses it. */}
                  <div data-role="desktop-feed-row" data-kind={card.kind} tabIndex={-1}>
                    <FeedCardItem
                      card={card}
                      now={now}
                      onAccept={onAccept}
                      seen={isSeen(card, lastSeenId)}
                      onOpenDetail={setOpenTicketId}
                      // "tap" is a phone word. The card is the same card — same
                      // clamp, same cue, same target — but a window that tells
                      // you to tap is the mobile-stretched reading this shell
                      // exists to replace (13 §4).
                      moreLabel="more"
                    />
                  </div>
                </li>
              ))}
            </ol>
          )}
          {/* Outside the empty/non-empty branch for the same reason as the phone:
              a feed whose cards have all collapsed away renders the resting
              state, and that is precisely where the way back has to stay. One
              control, one label — the desk collapses seen cards exactly like the
              phone does, so it gets exactly the same affordance (08 D2‴).

              Its placement is the phone's too, by the same two declarations
              (`margin-top: auto` + `position: sticky`, DesktopScreen.css over
              PrimaryScreen.css): the foot of the feed region in every state. The
              region used to say which state it was in — a `data-empty` attribute
              — because only the empty one put the control above the input; with
              the anchoring unconditional there was nothing left reading it. */}
          {hasEarlier && onShowEarlier !== undefined && (
            <button
              type="button"
              data-role="feed-show-earlier"
              onClick={onShowEarlier}
              disabled={loadingEarlier}
              aria-busy={loadingEarlier || undefined}
            >
              Show earlier
            </button>
          )}
        </section>

        {/* The input region. `position: relative` (CSS) makes it the containing
            block for the activity row, which is absolutely positioned at
            `bottom: 100%` — so Kiln's replies and its action toasts float just
            above the line, the same low-key register they have on mobile
            (13 §7). `--dock-overlay-height` is never published here (there is no
            dock), so the row's offset falls back to its 0px default. */}
        <div data-role="desktop-composer-region">
          <ActivityRow
            thinking={thinking}
            toasts={toasts}
            onDismiss={onDismiss}
            onOpenTicket={setOpenTicketId}
            onToastExpandedChange={onToastExpandedChange}
          />
          {composer}
        </div>
      </main>

      {openTicket !== null && (
        // Detail opens OVER the feed and gets out of the way when you're done
        // (13 D7) — no third pane, no inspector. The same sheet the mobile screen
        // opens, with the same actions wired the same way; a permanent detail
        // pane would double the resting complexity to serve something looked at
        // rarely, and re-create the two-pane console this design avoids.
        //
        // What the desk changes is the EDGE it comes from (13 D7a). A bottom
        // sheet is a phone's answer: it rises into the middle of the window and
        // covers the feed and the working strip — exactly the ongoing work the
        // user opened a ticket to read *against*. `placement="right"` anchors it
        // to the right edge at full height instead, so it reads as a panel
        // beside the work rather than a pop-up over it. Still an overlay, not a
        // third column: it takes no width from the feed while closed, and
        // closing it returns the window to its two regions untouched.
        <TicketDetail
          ticket={openTicket}
          surface="primary"
          placement="right"
          voiceControl={
            <TicketDetailVoiceActions
              ticketTitle={openTicket.title}
              onActiveChange={setTicketVoiceActive}
            />
          }
          voiceActive={ticketVoiceActive}
          transcript={<TicketDetailTranscript />}
          onClose={closeTicket}
          onAccept={(ticketId) => {
            onAccept(ticketId);
            closeTicket();
          }}
          onDelete={
            onDelete === undefined
              ? undefined
              : (ticketId) => {
                  onDelete(ticketId);
                  closeTicket();
                }
          }
          onPoke={
            onPoke === undefined
              ? undefined
              : (ticketId) => {
                  onPoke(ticketId);
                  closeTicket();
                }
          }
          onSetKeepSandbox={onSetKeepSandbox}
          onKillSandbox={onKillSandbox}
          onReassignSandbox={onReassignSandbox}
          sandboxStatus={openAgentStatus}
          canReassign={(board?.worker_free ?? 0) > 0}
          onEditText={onEditText}
        />
      )}
    </div>
  );
}
