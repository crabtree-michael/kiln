// The desktop shell, presentational (13 §3–§10). Pure props in → the whole
// two-region layout out, so tests render it directly with fixture data and never
// touch the live stores. `PrimaryScreen` bridges the same stores that feed the
// mobile view into these props.
//
// Three regions, left to right (13 §3, amended): the projects rail, the
// tickets panel, and the selected project's feed with the input under it.
// The middle column is the one addition to the original two-region shape, and it
// is deliberately NOT an inspector: it holds no selection and shows no detail.
// It answers two standing questions, in two sections — what is being worked on
// right now, and what is queued behind it (the ready pull queue and the
// proposals still being shaped, the same coverage the phone's header dropdown
// has always had). It is separated from the feed by a rule rather than by
// space, because unlike the rail (peripheral furniture, set apart by its
// recessed surface) it is content about the same project the feed is about, and
// the line is what says "these are two readings of one thing" instead of "this
// is more feed". That line is also the one piece of this layout the reader owns:
// it is a separator they can drag, between the width the column shipped at and
// twice it, because how much of a desk goes to titles and how much to the feed
// is a judgement about their project and their window, not one this file can
// make for them.
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
import { useRef, type JSX, type KeyboardEvent } from 'react';
import type {
  Board,
  ConnectionState,
  FeedSnapshot,
  NotificationModeValue,
} from '@/transport/transport';
import type { ActivityToast } from '@/stores/activity-context';
import type { WebPushStatus } from '@/stores/use-web-push';
import { FeedCardItem } from '@/components/FeedCardItem';
import type { TicketTextEdit } from '@/components/TicketDetail';
import { TicketDetailHost } from '@/components/TicketDetailHost';
import { ActivityRow } from '@/components/ActivityRow';
import { DesktopRail } from '@/components/desktop/DesktopRail';
import type { RailProject } from '@/components/desktop/ProjectsRail';
import { WorkingNow } from '@/components/desktop/WorkingNow';
import { blockedCount, workingTickets } from '@/components/desktop/working-now';
import { Backlog } from '@/components/desktop/Backlog';
import { backlogTickets } from '@/components/desktop/backlog';
import { useDesktopShellFlag } from '@/components/desktop/use-desktop-layout';
import {
  useWorkingPanelWidth,
  WORKING_PANEL_MAX_WIDTH,
  WORKING_PANEL_MIN_WIDTH,
} from '@/components/desktop/use-working-panel-width';
import { useTicketOverlay } from '@/components/use-ticket-overlay';
import { streamDetail } from '@/components/feed-format';
import { readFeed } from '@/components/feed-model';
import '@/components/PrimaryScreen.css';
import '@/components/desktop/DesktopScreen.css';

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
  // The same reading the phone gets, off the same function (see feed-model.ts):
  // the rows, which are seen, and where the "Earlier" divider falls. Two fields
  // this shell does NOT spend: `hasClearable` (no bulk clear on a desk) and each
  // row's `dismissId` (no swipe — 13 §6, and §13 Q3 is open). Having them in the
  // reading is not an invitation to grow either affordance here.
  const { summary, rows, isEmpty, lastWord } = readFeed(feed, lastSeenId, now);
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
  // …and what is queued up behind them: the ready pull queue, then the
  // proposals still being shaped. Off the same board snapshot, for the same
  // reason — a ticket can wait a long time without the brain having anything to
  // say about it, so the feed beside this column will never mention it.
  const waiting = backlogTickets(board);
  // Disconnected must be STATED, not hidden (13 §10): an ambient app that has
  // silently stopped receiving is worse than one that is visibly off. Low-key
  // and permanent while it lasts — never a modal, and deliberately not in the
  // accent, which is reserved for things needing a decision.
  const disconnected = connectionState === 'reconnecting';

  // The detail overlay's whole state cluster, shared with the phone and with
  // `/kanban` (see `use-ticket-overlay.ts`) — including the push deep link, which
  // works here exactly as it does on mobile (02 §10 / 12 §6.3): desktop being open
  // changes nothing about being found.
  const overlay = useTicketOverlay(board);
  const { setOpenTicketId } = overlay;

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

  // How wide the tickets column is — the shipped 248px as a floor, twice it as a
  // ceiling, and the separator beside the column as the way between them. The
  // live width is published as a custom property on the root below rather than
  // held here, so a drag costs no renders of the feed; see
  // use-working-panel-width.ts.
  const resize = useWorkingPanelWidth();

  return (
    <div
      data-role="desktop-screen"
      data-connection-state={connectionState}
      data-resizing={resize.dragging ? 'true' : undefined}
      ref={resize.shellRef}
    >
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

      {/* The working indication (13 §8.2), what it is working ON, and what is
          queued behind it — its own column, beside the feed rather than above
          it. A property of the project that stays true for as long as the work
          runs has no business inside (or on top of) a scrolling history: here it
          cannot be scrolled away, and it keeps its own reading rhythm instead of
          borrowing the feed's measure and reading as the first card.

          The two sections answer the two standing questions this column exists
          for — what is running, and what is next — and they are separate
          sections rather than one merged list because the difference between
          them is precisely what the reader is after. Only the first is live;
          only the first breathes.

          Scoped to the SELECTED project, like the feed beside it. The rail's
          per-project working counts come from a slow cross-project poll
          (`useProjectsStatus`, minutes stale by design) and naming tickets from
          it would put stale titles on screen next to a live feed; the board
          store behind this column is the live one.

          The column is always here, empty or not — see WorkingNow for why the
          geometry holds still. The backlog section under it is not: with an
          empty queue it renders nothing rather than a heading over a stated
          absence (see Backlog). That costs no geometry, because the column's
          width is fixed and the section is the last thing in it — what changes
          is how far down the panel ends, not where anything beside it sits.
          Breathing, slow and low-contrast; the per-ticket marks are the
          phone's, unchanged. See DesktopScreen.css. */}
      <div data-role="desktop-working-panel">
        <WorkingNow
          tickets={inProgress}
          blocked={blockedCount(board)}
          active={working}
          onOpenTicket={setOpenTicketId}
          now={now}
        />
        <Backlog tickets={waiting} onOpenTicket={setOpenTicketId} now={now} />
      </div>

      {/* The boundary between that column and the feed, and — since the two
          readings it separates are worth different amounts to different people
          on different windows — the handle that moves it. It is the rule itself
          rather than a mark beside it (DesktopScreen.css), so the resting screen
          gains nothing to look at: what it gains is a `col-resize` cursor when
          the pointer is over the line, which is the whole of how a split view
          announces itself on a desk.

          A `separator` with a value, not a slider and not a button: this is the
          ARIA window-splitter, so the keyboard path is the pointer's (arrows
          step the width, Home/End go to the ends) and `aria-valuenow` reports
          where the boundary actually is. The bounds are stated here because they
          never change; the value is written by the hook, which is the only thing
          that knows it — see use-working-panel-width.ts for why it is not a
          prop. */}
      <div
        data-role="desktop-working-resizer"
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize the tickets column"
        aria-valuemin={WORKING_PANEL_MIN_WIDTH}
        aria-valuemax={WORKING_PANEL_MAX_WIDTH}
        tabIndex={0}
        ref={resize.separatorRef}
        onPointerDown={resize.onPointerDown}
        onPointerMove={resize.onPointerMove}
        onPointerUp={resize.onPointerEnd}
        onPointerCancel={resize.onPointerEnd}
        onKeyDown={resize.onKeyDown}
      />

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
          {/* One sizing wrapper around everything this region scrolls, the pinned
              control included — the phone's `[data-role='feed-scroll']`, now on
              the desk (see DesktopScreen.css). On a touch screen it is held a
              hair taller than the scrollport so the feed is ALWAYS scrollable and
              the native rubber-band engages even when there is little in it; a
              tablet in landscape gets this shell, and without the pixel a short
              feed there simply doesn't answer the finger. It changes no geometry:
              the free height it takes is handed straight on to the resting block
              and to "Show earlier" below it. */}
          <div data-role="desktop-feed-scroll">
            {isEmpty ? (
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
                  <img
                    data-role="desktop-rest-mark"
                    src="/kiln-mark.svg"
                    alt=""
                    aria-hidden="true"
                  />
                  <p data-role="desktop-rest-line">All quiet.</p>
                  <p data-role="desktop-rest-detail">{streamDetail(summary)}</p>
                  {lastWord !== null && <p data-role="desktop-rest-subtext">{lastWord}</p>}
                </div>
              )
            ) : (
              <ol data-role="desktop-feed-list">
                {rows.map(({ card, seen, dividerBefore }) => (
                  <li key={card.id}>
                    {dividerBefore && (
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
                        seen={seen}
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
                the anchoring unconditional there was nothing left reading it.

                Last inside the scroll wrapper, not a sibling of it, and for the
                same reason it is last in the phone's backlog: that wrapper is the
                containing block its `position: sticky` hangs off. Lifted out to sit
                beside the wrapper it would stick to a box it no longer belongs to
                and stop being the foot of the region. */}
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
          </div>
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

      {/* Detail opens OVER the feed and gets out of the way when you're done
          (13 D7) — no third pane, no inspector. The same sheet the mobile screen
          opens, with the same actions wired the same way, from the same host; a
          permanent detail pane would double the resting complexity to serve
          something looked at rarely, and re-create the two-pane console this
          design avoids.

          What the desk changes is the EDGE it comes from (13 D7a). A bottom
          sheet is a phone's answer: it rises into the middle of the window and
          covers the feed and the working strip — exactly the ongoing work the
          user opened a ticket to read *against*. `placement="right"` anchors it
          to the right edge at full height instead, so it reads as a panel beside
          the work rather than a pop-up over it. Still an overlay, not a third
          column: it takes no width from the feed while closed, and closing it
          returns the window to its two regions untouched. */}
      <TicketDetailHost
        overlay={overlay}
        placement="right"
        onAccept={onAccept}
        onDelete={onDelete}
        onPoke={onPoke}
        onSetKeepSandbox={onSetKeepSandbox}
        onKillSandbox={onKillSandbox}
        onReassignSandbox={onReassignSandbox}
        onEditText={onEditText}
      />
    </div>
  );
}
