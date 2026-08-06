// The primary screen, presentational (08 §2–§5). Pure props in → the whole
// selector surface out, so the DOM-snapshot tests render it directly with
// fixture data and never touch the live stores. `PrimaryScreen` (the composing
// wrapper) bridges the feed + activity stores into these props.
import { useCallback, useRef, useState, type JSX } from 'react';
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
import { SwipeToDismiss } from '@/components/SwipeToDismiss';
import { TicketDetail, type TicketTextEdit } from '@/components/TicketDetail';
import { TicketDetailTranscript } from '@/components/TicketDetailTranscript';
import { TicketDetailVoiceActions } from '@/components/TicketDetailVoiceActions';
import { ActivityRow } from '@/components/ActivityRow';
import { Dock } from '@/components/Dock';
import { HeaderStatusMenu } from '@/components/HeaderStatusMenu';
import { NotificationSettingsMenu } from '@/components/NotificationSettingsMenu';
import { lastWordDetail, streamDetail } from '@/components/feed-format';
import { useDeepLinkTicket } from '@/components/use-deep-link-ticket';
import { usePullToRefresh } from '@/components/use-pull-to-refresh';
import '@/components/PrimaryScreen.css';

const EMPTY_SUMMARY: FeedSummary = {
  blocker_count: 0,
  update_count: 0,
  stream_count: 0,
  building: 0,
  idle: 0,
};

export interface PrimaryScreenViewProps {
  feed: FeedSnapshot | null;
  /** The header brand slot (12 §4.1). When provided, the composing screen passes
   * the `ProjectSwitcher`, which turns the "Kiln" wordmark into the project-switcher
   * trigger. Omitted (presentational tests) renders the static "Kiln" mark, so the
   * header DOM/snapshots stay byte-for-byte unchanged. */
  brand?: JSX.Element | undefined;
  /** The latest board snapshot, broken out per-ticket in the header dropdown.
   * Optional so presentational tests can omit it (the menu then shows no
   * tickets). */
  board?: Board | null;
  connectionState: ConnectionState;
  thinking: boolean;
  toasts: ActivityToast[];
  onDismiss: (id: number) => void;
  /** Pauses a `say` pill's auto-dismiss timer when the user opens it to read the
   * full message (08 §4). Optional so presentational tests can omit the store wiring. */
  onToastExpandedChange?: ((id: number, expanded: boolean) => void) | undefined;
  onAccept: (ticketId: string) => void;
  /** Delete a proposal that's no longer wanted — the ticket detail's "Delete"
   * action, shown only on a shaping ticket. The composing screen routes this
   * through the brain (D5, delete_ticket); omitted (presentational tests) leaves
   * the sheet without a Delete button, so the DOM/snapshots stay unchanged. */
  onDelete?: ((ticketId: string) => void) | undefined;
  /** Nudge a stalled agent to continue — the ticket detail's "👉 Poke"
   * action, shown on working/blocked tickets. The composing screen routes this
   * through the brain (D5); omitted (presentational tests) leaves the sheet without
   * a Poke button. */
  onPoke?: ((ticketId: string) => void) | undefined;
  /** Save (or stop saving) the open ticket's sandbox — the ticket detail's
   * sandbox switch, shown on every state. The composing screen writes it straight
   * to the board (a setting, not a transition, so it does NOT go through the
   * brain); omitted (presentational tests) leaves the sheet without the switch, so
   * the DOM/snapshots stay unchanged. */
  onSetKeepSandbox?: ((ticketId: string, keep: boolean) => void) | undefined;
  /** Destroy the open ticket's sandbox — the ticket detail's "Kill sandbox"
   * action, shown on working/blocked tickets. The manual override for a wedged or
   * corrupted workspace; the composing screen writes it straight to the board
   * (not through the brain — an override that waits on an LLM turn isn't one).
   * Omitted (presentational tests) leaves the sheet without the button. */
  onKillSandbox?: ((ticketId: string) => void) | undefined;
  /** Move the open ticket to a different sandbox and restart it there — the
   * ticket detail's "Move to a new sandbox" action, the recovery counterpart to
   * the kill. Also a direct board write. Omitted leaves the sheet without it. */
  onReassignSandbox?: ((ticketId: string) => void) | undefined;
  /** Save the user's own edit of the open ticket's title/body — reached by
   * pressing the ticket detail's body, on a backlog ticket (shaping/ready).
   * The composing screen writes the text straight to the board; it deliberately
   * does NOT go through the brain, since an LLM pass between the user and their
   * own words is the drift a direct edit exists to remove. Omitted
   * (presentational tests) leaves the sheet's body inert, so the DOM/snapshots
   * stay unchanged. */
  onEditText?: ((ticketId: string, patch: TicketTextEdit) => void) | undefined;
  /** Clear a single update/preview card by its notification id — the swipe-left
   * gesture (08 §3). When provided, notification-backed cards become swipeable;
   * omitted (presentational tests) leaves every card static, so the swipe wrapper
   * and its DOM are absent unless wired. */
  onDismissCard?: ((notificationId: number) => void) | undefined;
  /** Clear ALL notification-backed cards at once — the header trash affordance
   * (08 §3). When provided, a trash button appears beside the bell; the click
   * confirms first, then clears. Omitted (presentational tests) leaves the button
   * absent, mirroring how `onDismissCard` gates the swipe wrapper. */
  onDismissAll?: (() => void) | undefined;
  /** Fired when the tickets dropdown opens — triggers an independent board
   * refresh so the ticket list isn't stale until the next agent push.
   * Optional so presentational tests can omit it. */
  onOpenTickets?: (() => void) | undefined;
  /** True while that refresh is in flight, so the dropdown can show a loading
   * indicator instead of a blank/empty state. */
  ticketsRefreshing?: boolean;
  /** The last-seen divider boundary (08 D2′): update/preview cards with a greater
   * `notification_id` are new since the last visit; those at or below it are
   * older history. `null` (default) shows no divider. */
  lastSeenId?: number | null;
  /** True when there is anything further back to show (08 D2‴) — already-seen
   * cards collapsed out of the feed, or older history still on the server. Shows
   * the single "Show earlier" control at the foot of the feed. */
  hasEarlier?: boolean;
  /** True while that control has a history page fetch in flight (it disables,
   * but never renames itself — there is one label). */
  loadingEarlier?: boolean;
  /** Bring back what the feed has tucked away (08 D2‴) — the collapsed seen
   * cards first, then older history. Omitted (presentational tests) leaves the
   * control absent, mirroring how `onDismissCard` gates the swipe wrapper. */
  onShowEarlier?: (() => void) | undefined;
  /** Re-fetch the whole feed — the pull-to-refresh gesture. When provided, a
   * downward pull from the top of the feed spins up a refresh indicator and
   * re-fetches; the returned promise keeps the indicator up until the fetch
   * settles. Omitted (presentational tests) leaves the gesture and its indicator
   * DOM absent, mirroring how `onDismissCard` gates the swipe wrapper. */
  onRefreshFeed?: (() => Promise<void>) | undefined;
  /** The current push-notification frequency, shown selected in the bell menu
   * (02 §10). Defaults to `blocked` (the current behavior) when omitted. */
  notificationMode?: NotificationModeValue;
  /** Persist a new push-notification frequency. Optional so presentational tests
   * can omit it (the bell menu's options then render disabled). */
  onSelectNotificationMode?: ((mode: NotificationModeValue) => void) | undefined;
  /** The browser + backend push capability, for the bell menu's permission
   * button. Optional; omitted renders it as "checking". */
  pushStatus?: WebPushStatus | undefined;
  /** Request OS notification permission + register for push (02 §10). Optional. */
  onEnablePush?: (() => void) | undefined;
  /** Turn push back off (unsubscribe this browser). Optional. */
  onDisablePush?: (() => void) | undefined;
  /** Injected "now" for deterministic relative-age rendering (defaults to real time). */
  now?: number;
}

/** An update/preview card's numeric notification_id, or null for board cards.
 * Drives the last-seen divider — the "new since last visit" boundary is about
 * brain-authored update/preview history, so the mechanical poke/done notices
 * stay out of it. */
function updateId(card: FeedCard): number | null {
  const isUpdate = card.kind === 'update' || card.kind === 'preview';
  return isUpdate && typeof card.notification_id === 'number' ? card.notification_id : null;
}

/** The numeric notification_id of a card the user can swipe to clear, or null.
 * Every notification-backed card — the brain-authored update/preview cards, the
 * runtime's "done" completion notice, and the steward's "poke" stall nudge — is
 * a stray notification the user can wave off once read. Only blockers stay put:
 * a blocker demands an explicit decision, not a swipe. Board cards
 * (blocker/proposal) carry no notification_id, so they never gain the gesture. */
function dismissableId(card: FeedCard): number | null {
  const isDismissable =
    card.kind === 'update' ||
    card.kind === 'preview' ||
    card.kind === 'done' ||
    card.kind === 'poke';
  return isDismissable && typeof card.notification_id === 'number' ? card.notification_id : null;
}

/** The index of the first update card at/below the last-seen boundary — the
 * "last seen" divider position (08 D2′), separating new-since-last-visit updates
 * above from older history below. Shown only when there is at least one newer
 * update above the boundary AND `lastSeenId` is known. Returns -1 otherwise. */
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

/** Whether a card sits at/below the last-seen boundary — already-seen history
 * that renders de-emphasized (unbolded title, body collapsed tighter) so the
 * new-since-last-visit cards above stay the feed's focus (08 D2′). Board cards
 * (blocker/proposal, no `notification_id`) never recede — they still need the
 * user. Returns false when no boundary is known (fresh visit / nothing seen). */
function isSeen(card: FeedCard, lastSeenId: number | null): boolean {
  if (lastSeenId === null) {
    return false;
  }
  const id = updateId(card);
  return id !== null && id <= lastSeenId;
}

/** The full ticket a proposal card points at, looked up in the board snapshot by
 * id (08 §5). Proposals are Shaping tickets, but every bucket is scanned so a
 * ticket that moves state between the click and the render still resolves.
 * Returns null before the first board snapshot lands or if the id is gone. */
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

export function PrimaryScreenView({
  feed,
  brand,
  board = null,
  connectionState,
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
  onDismissCard,
  onDismissAll,
  onOpenTickets,
  ticketsRefreshing = false,
  lastSeenId = null,
  hasEarlier = false,
  loadingEarlier = false,
  onShowEarlier,
  onRefreshFeed,
  notificationMode = 'blocked',
  onSelectNotificationMode,
  pushStatus,
  onEnablePush,
  onDisablePush,
  now = Date.now(),
}: PrimaryScreenViewProps): JSX.Element {
  const summary = feed?.summary ?? EMPTY_SUMMARY;
  const cards = feed?.cards ?? [];
  const isEmpty = cards.length === 0;
  // The all-clear subtext, null when the brain has never spoken (no line at all).
  const lastWord = lastWordDetail(summary, now);
  const divider = dividerIndex(cards, lastSeenId);
  // Whether any notification-backed card is present — the trash affordance clears
  // those (blockers/proposals are board state, untouched), so it's disabled when
  // there is nothing to clear.
  const hasClearable = cards.some((card) => card.kind !== 'blocker' && card.kind !== 'proposal');

  // Which proposal's full ticket is open in the click-through detail overlay
  // (08 §5). View-only state held here, mirroring how Board owns its selected
  // ticket. The id is resolved against the live board each render, so the overlay
  // drains on its own if the ticket leaves the board (e.g. after Accept).
  const [openTicketId, setOpenTicketId] = useState<string | null>(null);
  // The ticket detail overlay is opened from a feed card, the header menu, a
  // push-notification deep link, or a tapped board `toast` on the activity row
  // (08 §4) — the toast dismisses itself through its own auto-dismiss/`onDismiss`
  // path, so opening the overlay is a plain setter with no toast state to
  // reconcile here. `closeTicket` is a stable callback because the sheet + several
  // in-sheet actions all close through it.
  const closeTicket = useCallback((): void => {
    setOpenTicketId(null);
  }, []);
  // A tapped push notification deep-links here (02 §10): open the ticket it names,
  // whether we were opened fresh at `/app?ticket=<id>` or handed the tap live by the
  // service worker. The id resolves against the board below like any other open.
  useDeepLinkTicket(setOpenTicketId);
  // Whether the open sheet has a live voice session in it, reported up from the
  // sheet's voice cluster (`TicketDetailVoiceActions`). It rearranges the sheet's
  // footer — the mic crosses to the trailing group to sit beside Send and ×, and
  // Accept stands down for the duration. Held here rather than read from the voice
  // store because this screen is not a voice consumer: the cluster hands up a
  // *boolean*, so this re-renders when the footer's shape changes rather than once
  // a spoken word.
  const [ticketVoiceActive, setTicketVoiceActive] = useState(false);
  const openTicket = findTicket(board, openTicketId);
  // The open ticket's bound agent, looked up in the board snapshot's `agents`
  // join (keyed by ticket_id). It reaches the sheet as the gear menu's status
  // line and nothing else — Poke used to be gated on it reading `idle`, which hid
  // the button on exactly the in-progress tickets the user wanted to nudge.
  const openAgentStatus =
    openTicket === null
      ? undefined
      : board?.agents.find((agent) => agent.ticket_id === openTicket.id)?.status;
  // Pull-to-refresh: the feed section is the scroll container, so the gesture
  // reads its scrollTop off this ref. Only wired when `onRefreshFeed` is provided
  // (the composing screen passes it; presentational tests omit it, leaving the
  // indicator DOM absent so snapshots are unchanged).
  const feedRef = useRef<HTMLElement>(null);
  const { pull, refreshing, dragging } = usePullToRefresh(feedRef, onRefreshFeed);

  return (
    <div data-role="primary-screen" data-connection-state={connectionState}>
      {/* The nav bar lives OUTSIDE the scrolling feed region so it stays pinned to
          the physical top in every scroll state. When it sat inside the feed
          (position: sticky), an overscroll/rubber-band at the top of the feed
          dragged the whole scroll content — the header with it — down, revealing
          blank space above the nav bar. As a flex sibling above the feed it can't
          be pulled: the elastic bounce now shows as blank space inside the feed
          scrollport, below the pinned bar. */}
      <header data-role="feed-header">
        {brand ?? (
          <div data-role="kiln-mark">
            <img data-role="kiln-glyph" src="/kiln-mark.svg" alt="" aria-hidden="true" />
            <span data-role="kiln-wordmark">Kiln</span>
          </div>
        )}
        <div data-role="header-actions">
          <NotificationSettingsMenu
            mode={notificationMode}
            onSelectMode={onSelectNotificationMode}
            pushStatus={pushStatus}
            onEnablePush={onEnablePush}
            onDisablePush={onDisablePush}
          />
          {/* Quick hop to the account view. A plain anchor, not a router `Link`:
              `/dashboard` mounts its own provider tree (no SessionProvider) and
              this view is deliberately router-free — the one router-dependent
              header control (the ProjectSwitcher) is injected through the brand
              slot rather than imported here. */}
          <a data-role="header-dashboard" href="/dashboard" aria-label="Dashboard">
            <svg data-role="header-gear" viewBox="0 0 20 20" aria-hidden="true">
              <path
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
                d="M9.00 2.87A7.2 7.2 0 0 1 11.00 2.87L11.31 4.76A5.4 5.4 0 0 1 12.78 5.37L14.33 4.25A7.2 7.2 0 0 1 15.75 5.67L14.63 7.22A5.4 5.4 0 0 1 15.24 8.69L17.13 9.00A7.2 7.2 0 0 1 17.13 11.00L15.24 11.31A5.4 5.4 0 0 1 14.63 12.78L15.75 14.33A7.2 7.2 0 0 1 14.33 15.75L12.78 14.63A5.4 5.4 0 0 1 11.31 15.24L11.00 17.13A7.2 7.2 0 0 1 9.00 17.13L8.69 15.24A5.4 5.4 0 0 1 7.22 14.63L5.67 15.75A7.2 7.2 0 0 1 4.25 14.33L5.37 12.78A5.4 5.4 0 0 1 4.76 11.31L2.87 11.00A7.2 7.2 0 0 1 2.87 9.00L4.76 8.69A5.4 5.4 0 0 1 5.37 7.22L4.25 5.67A7.2 7.2 0 0 1 5.67 4.25L7.22 5.37A5.4 5.4 0 0 1 8.69 4.76L9.00 2.87Z"
              />
              <circle cx="10" cy="10" r="2.4" fill="none" stroke="currentColor" strokeWidth="1.5" />
            </svg>
          </a>
          {onDismissAll !== undefined && (
            <button
              type="button"
              data-role="feed-clear-all"
              aria-label="Clear all notifications"
              disabled={!hasClearable}
              onClick={() => {
                // A confirm before an irreversible bulk clear; cancelling leaves
                // the feed untouched (08 §3).
                if (window.confirm('Clear all notifications?')) {
                  onDismissAll();
                }
              }}
            >
              <svg data-role="clear-all-trash" viewBox="0 0 20 20" aria-hidden="true">
                <path
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  d="M4 6h12M8.5 6V4.8a1 1 0 0 1 1-1h1a1 1 0 0 1 1 1V6M6.3 6l.6 9.4a1 1 0 0 0 1 .9h4.2a1 1 0 0 0 1-.9l.6-9.4M9 9.2v4.3M11 9.2v4.3"
                />
              </svg>
            </button>
          )}
          <HeaderStatusMenu
            summary={summary}
            board={board}
            onOpen={onOpenTickets}
            refreshing={ticketsRefreshing}
            // Selecting a ticket from the dropdown drives the same detail
            // overlay as a proposal card / deep link (08 §5): the id resolves
            // against the live board below into the TicketDetail sheet.
            onSelectTicket={setOpenTicketId}
          />
        </div>
      </header>
      <section
        ref={feedRef}
        role="region"
        aria-label="Feed"
        data-role="feed"
        data-connection-state={connectionState}
      >
        {/* Pull-to-refresh indicator: an in-flow strip above the backlog whose
            height follows the pull (and rests open while the refresh is in
            flight), so growing it pushes the feed down under the finger like a
            native rubber-band. Rendered only when the gesture is wired, so the
            presentational DOM/snapshots are unchanged when it isn't. */}
        {onRefreshFeed !== undefined && (
          <div
            data-role="feed-pull"
            data-refreshing={refreshing ? 'true' : undefined}
            data-dragging={dragging ? 'true' : undefined}
            aria-hidden={pull > 0 || refreshing ? undefined : true}
            style={{ height: `${String(pull)}px` }}
          >
            <span data-role="feed-pull-spinner" data-spinning={refreshing ? 'true' : undefined} />
          </div>
        )}
        {/* Single sizing wrapper for everything that scrolls (the backlog). It is
            held a hair taller than the feed scrollport (see [data-role='feed-scroll']
            in PrimaryScreen.css) so the feed is always scrollable and the native
            rubber-band engages even when the backlog is short or empty — the app
            feels elastic instead of stuck. */}
        <div data-role="feed-scroll">
          <div data-role="backlog">
            {isEmpty ? (
              <div data-role="feed-empty">
                <img data-role="feed-empty-mark" src="/kiln-mark.svg" alt="" aria-hidden="true" />
                <div data-role="feed-empty-status">
                  <div data-role="feed-empty-status-line">
                    <span
                      data-role="feed-empty-pulse"
                      data-active={summary.building > 0}
                      aria-hidden="true"
                    />
                    <span>{streamDetail(summary)}</span>
                  </div>
                  {lastWord !== null && <span data-role="feed-empty-subtext">{lastWord}</span>}
                </div>
              </div>
            ) : (
              <>
                {cards.map((card, index) => {
                  // Every notification-backed card can be cleared: update/
                  // preview, the runtime's "done" notice, and the steward's
                  // "poke" nudge. Blockers/proposals (board state the brain
                  // owns) carry no notification_id and stay static — a blocker
                  // needs an explicit decision, not a swipe.
                  const dismissId = dismissableId(card);
                  const item = (
                    <FeedCardItem
                      card={card}
                      now={now}
                      onAccept={onAccept}
                      seen={isSeen(card, lastSeenId)}
                      onOpenDetail={setOpenTicketId}
                    />
                  );
                  return (
                    <div key={card.id} data-role="backlog-slot">
                      {index === divider && (
                        <div data-role="feed-divider" data-variant="last-seen">
                          Earlier
                        </div>
                      )}
                      {onDismissCard !== undefined && dismissId !== null ? (
                        <SwipeToDismiss
                          onDismiss={() => {
                            onDismissCard(dismissId);
                          }}
                        >
                          {item}
                        </SwipeToDismiss>
                      ) : (
                        item
                      )}
                    </div>
                  );
                })}
              </>
            )}
            {/* The ONE way back to what the feed has tucked away (08 D2‴):
                already-seen cards collapse out of it, and older history is a
                page away on the server. One control, one label — it always means
                "further back", so it never has to say which of the two it is
                about to do. Deliberately OUTSIDE the empty/non-empty branch:
                collapsing the last card leaves the feed rendering "All clear",
                and that is exactly the state where the user most needs a way
                back to what was just there.

                Last in the backlog, and it stays the foot of the FEED REGION
                from here — not merely the end of the scrolled content, which is
                all this position used to buy. The stylesheet does that half with
                one `margin-top: auto` (cards short of the scrollport) and one
                `position: sticky` (cards past it), so the control is above the
                dock at every card count and every scroll offset. Nothing about
                the markup encodes the placement, so don't move this out of the
                backlog to "fix" it — that would take the sticky element out of
                the containing block the anchoring depends on. */}
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
        </div>
      </section>

      {/* The dock region is the in-flow bottom anchor; its height is exactly the
          dock's, because the activity row (toasts) and the live transcript are
          both lifted out of flow as overlays that grow UPWARD over the feed (see
          PrimaryScreen.css). That keeps a multi-line toast or a long transcript
          from shrinking the flex:1 feed and reflowing the empty state / backlog. */}
      <div data-role="dock-region">
        <ActivityRow
          thinking={thinking}
          toasts={toasts}
          onDismiss={onDismiss}
          onOpenTicket={setOpenTicketId}
          onToastExpandedChange={onToastExpandedChange}
        />
        {/* The permanent error band is rendered INSIDE the dock (as its first
            child), not here — so the live-transcript overlay grows above it rather
            than painting over it. It still reserves its own space above the dock's
            controls; empty alerts render nothing, leaving the idle layout
            untouched. */}
        <Dock alerts={board?.alerts ?? []} />
      </div>

      {openTicket !== null && (
        <TicketDetail
          ticket={openTicket}
          surface="primary"
          // The sheet's voice cluster, shown on every ticket state — the unified
          // communication surface (08 §5) that replaces the old blocked-only "Talk
          // to unblock" button, so the user can start talking to the brain directly
          // from any ticket. At rest it is the same dock orb at the footer's
          // bottom-left, tapped to start a session without leaving the sheet; while
          // one is live it brings Send and a discard (×) with it across to the
          // row's trailing end, and reports that up through `onActiveChange` so the
          // sheet can stand Accept down for the duration. Safe to always pass — it
          // only mounts (and touches the voice store) when the sheet renders it.
          // `ticketTitle` registers this ticket with the voice store so whatever the
          // user sends from the sheet is prefixed with it, giving the brain the
          // context of what they're commenting on (08 §5).
          voiceControl={
            <TicketDetailVoiceActions
              ticketTitle={openTicket.title}
              onActiveChange={setTicketVoiceActive}
            />
          }
          voiceActive={ticketVoiceActive}
          // The live transcript for that mic, shown in the sheet's dock above the
          // controls so the user watches their words land without leaving the sheet
          // (08 §5). Self-gating (renders nothing until there is text) and rides the
          // same gate as the mic (any state), so it is safe to always pass — it only
          // touches the voice store while the sheet renders it.
          transcript={<TicketDetailTranscript />}
          onClose={closeTicket}
          // Accept is a proposal action; TicketDetail only surfaces it while the
          // ticket is still shaping, so it's safe to always wire — the sheet decides.
          onAccept={(ticketId) => {
            onAccept(ticketId);
            closeTicket();
          }}
          // Delete only surfaces on a shaping proposal (TicketDetail gates it):
          // route the deletion through the brain, then close the sheet like Accept
          // — the proposal's removal comes back over the stream. Omitted when the
          // composing screen didn't wire it, so no button shows (presentational).
          onDelete={
            onDelete === undefined
              ? undefined
              : (ticketId) => {
                  onDelete(ticketId);
                  closeTicket();
                }
          }
          // Poke surfaces on working/blocked tickets (TicketDetail gates it):
          // route the "continue" intent through the brain, then close the sheet
          // like Accept — the resulting agent activity comes back over the stream.
          // Omitted when the composing screen didn't wire it, so no button shows.
          onPoke={
            onPoke === undefined
              ? undefined
              : (ticketId) => {
                  onPoke(ticketId);
                  closeTicket();
                }
          }
          // The sandbox switch shows on every ticket state. Passed straight
          // through — unlike Accept/Delete/Poke it does NOT close the sheet: it
          // is a setting the user flips while reading, not an action that ends
          // the visit, and the new value arrives on the next board snapshot.
          onSetKeepSandbox={onSetKeepSandbox}
          // The manual sandbox overrides, shown only on a working/blocked ticket
          // (TicketDetail gates them) and, like the switch above, leaving the
          // sheet open — the user is dealing with a broken sandbox and wants to
          // watch what happens to it, not be thrown back to the feed.
          onKillSandbox={onKillSandbox}
          onReassignSandbox={onReassignSandbox}
          // The sandbox's own session status, and whether there is a free one to
          // move to. Both come from the board snapshot the sheet is already
          // rendering, so the controls describe the real sandbox rather than
          // guessing from the ticket's column.
          sandboxStatus={openAgentStatus}
          canReassign={(board?.worker_free ?? 0) > 0}
          // The body is pressable only on a backlog ticket (TicketDetail gates it).
          // Passed straight through — like the sandbox switch, and unlike
          // Accept/Delete/Poke, saving an edit does NOT close the sheet: the
          // user corrected the wording and should see the corrected ticket, not
          // be thrown back to the feed.
          onEditText={onEditText}
        />
      )}
    </div>
  );
}
