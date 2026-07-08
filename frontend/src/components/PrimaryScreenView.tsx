// The primary screen, presentational (08 §2–§5). Pure props in → the whole
// selector surface out, so the DOM-snapshot tests render it directly with
// fixture data and never touch the live stores. `PrimaryScreen` (the composing
// wrapper) bridges the feed + activity stores into these props, mirroring how
// `App` bridges its stores into `Board`/`ChatPanel`.
import { useRef, useState, type JSX } from 'react';
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
import { TicketDetail } from '@/components/TicketDetail';
import { ActivityRow } from '@/components/ActivityRow';
import { Dock } from '@/components/Dock';
import { HeaderStatusMenu } from '@/components/HeaderStatusMenu';
import { NotificationSettingsMenu } from '@/components/NotificationSettingsMenu';
import { streamDetail } from '@/components/feed-format';
import { useDeepLinkTicket } from '@/components/use-deep-link-ticket';
import { usePullToRefresh } from '@/components/use-pull-to-refresh';
import { useVoice } from '@/voice/voice-context';
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
  /** The latest board snapshot, broken out per-ticket in the header dropdown.
   * Optional so presentational tests can omit it (the menu then shows no
   * tickets). */
  board?: Board | null;
  connectionState: ConnectionState;
  thinking: boolean;
  toasts: ActivityToast[];
  onDismiss: (id: number) => void;
  onAccept: (ticketId: string) => void;
  /** Nudge a stalled agent to continue — the ticket detail's "Poke to continue"
   * action, shown on working/blocked tickets. The composing screen routes this
   * through the brain (D5); omitted (presentational tests) leaves the sheet without
   * a Poke button. */
  onPoke?: ((ticketId: string) => void) | undefined;
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
  /** True when older retained update history remains to page in (08 D2′) — shows
   * the "Show earlier updates" affordance at the foot of the feed. */
  hasMoreHistory?: boolean;
  /** True while a history page fetch is in flight (button shows a loading label). */
  loadingMoreHistory?: boolean;
  /** Fetch and append the next older page of update history (08 D2′). */
  onLoadMoreHistory?: (() => void) | undefined;
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
  board = null,
  connectionState,
  thinking,
  toasts,
  onDismiss,
  onAccept,
  onPoke,
  onDismissCard,
  onDismissAll,
  onOpenTickets,
  ticketsRefreshing = false,
  lastSeenId = null,
  hasMoreHistory = false,
  loadingMoreHistory = false,
  onLoadMoreHistory,
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
  // A tapped push notification deep-links here (02 §10): open the ticket it names,
  // whether we were opened fresh at `/?ticket=<id>` or handed the tap live by the
  // service worker. The id resolves against the board below like any other open.
  useDeepLinkTicket(setOpenTicketId);
  const openTicket = findTicket(board, openTicketId);
  // The open ticket's bound agent, looked up in the board snapshot's `agents`
  // join (keyed by ticket_id). Its session status gates the Poke button: a
  // *working* ticket only offers "Poke to continue" once the agent is `idle`
  // (alive, between turns, waiting) — never while a turn is streaming
  // (`building`), so the user isn't invited to nudge an agent already moving.
  const openAgentIdle =
    openTicket !== null &&
    board?.agents.find((agent) => agent.ticket_id === openTicket.id)?.status === 'idle';
  // The mic control, shared with the dock. A blocked ticket's detail hands off
  // to it: tapping Talk closes the sheet (so the single dock voice surface is no
  // longer covered) and turns the mic on, dropping the user straight into a
  // spoken exchange with the brain about how to unblock the work.
  const { resume } = useVoice();

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
        <div data-role="kiln-mark">
          <img data-role="kiln-glyph" src="/kiln-mark.svg" alt="" aria-hidden="true" />
          <span data-role="kiln-wordmark">Kiln</span>
        </div>
        <div data-role="header-actions">
          <NotificationSettingsMenu
            mode={notificationMode}
            onSelectMode={onSelectNotificationMode}
            pushStatus={pushStatus}
            onEnablePush={onEnablePush}
            onDisablePush={onDisablePush}
          />
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
                  <span
                    data-role="feed-empty-pulse"
                    data-active={summary.building > 0}
                    aria-hidden="true"
                  />
                  <span>{streamDetail(summary, now)}</span>
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
                {hasMoreHistory && onLoadMoreHistory !== undefined && (
                  <button
                    type="button"
                    data-role="feed-load-more"
                    onClick={onLoadMoreHistory}
                    disabled={loadingMoreHistory}
                  >
                    {loadingMoreHistory ? 'Loading…' : 'Show earlier updates'}
                  </button>
                )}
              </>
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
        <ActivityRow thinking={thinking} toasts={toasts} onDismiss={onDismiss} />
        <Dock />
      </div>

      {openTicket !== null && (
        <TicketDetail
          ticket={openTicket}
          surface="primary"
          // Only a working ticket whose agent has gone idle offers Poke; while the
          // agent is mid-turn (progress streaming) the button stays hidden.
          agentIdle={openAgentIdle}
          onClose={() => {
            setOpenTicketId(null);
          }}
          // Accept is a proposal action; TicketDetail only surfaces it while the
          // ticket is still shaping, so it's safe to always wire — the sheet decides.
          onAccept={(ticketId) => {
            onAccept(ticketId);
            setOpenTicketId(null);
          }}
          // Talk only surfaces on a blocked ticket (TicketDetail gates it):
          // close the sheet to uncover the dock and open the mic for unblocking.
          onTalk={() => {
            setOpenTicketId(null);
            resume();
          }}
          // Poke surfaces on working/blocked tickets (TicketDetail gates it):
          // route the "continue" intent through the brain, then close the sheet
          // like Accept — the resulting agent activity comes back over the stream.
          // Omitted when the composing screen didn't wire it, so no button shows.
          onPoke={
            onPoke === undefined
              ? undefined
              : (ticketId) => {
                  onPoke(ticketId);
                  setOpenTicketId(null);
                }
          }
        />
      )}
    </div>
  );
}
