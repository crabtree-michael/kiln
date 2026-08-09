// The primary screen, presentational (08 §2–§5). Pure props in → the whole
// selector surface out, so the DOM-snapshot tests render it directly with
// fixture data and never touch the live stores. `PrimaryScreen` (the composing
// wrapper) bridges the feed + activity stores into these props.
import { useRef, type JSX } from 'react';
import type { Board, ConnectionState, FeedSnapshot } from '@/transport/transport';
import type { ActivityToast } from '@/stores/activity-context';
import { FeedCardItem } from '@/components/FeedCardItem';
import { SwipeToDismiss } from '@/components/SwipeToDismiss';
import type { TicketTextEdit } from '@/components/TicketDetail';
import { TicketDetailHost } from '@/components/TicketDetailHost';
import { ActivityRow } from '@/components/ActivityRow';
import { Dock } from '@/components/Dock';
import { HeaderStatusMenu } from '@/components/HeaderStatusMenu';
import { streamDetail } from '@/components/feed-format';
import { readFeed } from '@/components/feed-model';
import { useTicketOverlay } from '@/components/use-ticket-overlay';
import { usePullToRefresh } from '@/components/use-pull-to-refresh';
import '@/components/PrimaryScreen.css';

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
  /** Injected "now" for deterministic relative-age rendering (defaults to real time). */
  now?: number;
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
  onOpenTickets,
  ticketsRefreshing = false,
  lastSeenId = null,
  hasEarlier = false,
  loadingEarlier = false,
  onShowEarlier,
  onRefreshFeed,
  now = Date.now(),
}: PrimaryScreenViewProps): JSX.Element {
  // The feed, already decided (see feed-model.ts): which rows there are, which
  // are seen, which can be swiped away, and where the "Earlier" divider falls.
  // This shell chooses the elements and nothing else.
  const { summary, rows, isEmpty, lastWord } = readFeed(feed, lastSeenId, now);

  // The click-through detail overlay (08 §5) — which ticket is open, how it
  // closes, the push deep-link, and whether a voice session is live inside it.
  // Shared with both desktop shells; see `use-ticket-overlay.ts`. It is opened
  // from a feed card, the header dropdown, a deep link, or a tapped board `toast`
  // on the activity row, all through the same setter.
  const overlay = useTicketOverlay(board);
  const { setOpenTicketId } = overlay;
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
        {/* One control, and no more: the tickets dropdown. The bulk-clear trash
            left this bar once notifications began clearing themselves — a manual
            clear of what is already going away is clutter, and the per-card swipe
            (08 §3) still handles the one card someone wants gone now. The bell
            menu and the standalone gear left before it: the account view is
            reached through the project switcher's own "Settings" item (which is
            where the notification settings live too), so the top bar carries only
            what acts on what is on screen. */}
        <div data-role="header-actions">
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
                {rows.map(({ card, seen, dismissId, dividerBefore }) => {
                  const item = (
                    <FeedCardItem
                      card={card}
                      now={now}
                      onAccept={onAccept}
                      seen={seen}
                      onOpenDetail={setOpenTicketId}
                    />
                  );
                  return (
                    <div key={card.id} data-role="backlog-slot">
                      {dividerBefore && (
                        <div data-role="feed-divider" data-variant="last-seen">
                          Earlier
                        </div>
                      )}
                      {/* The swipe wrapper is gated on BOTH the callback being
                          wired and the card being notification-backed: a
                          blocker demands an explicit decision, not a swipe, and
                          a presentational test that omits the callback must see
                          no wrapper at all (the DOM/snapshots depend on it). */}
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

      <TicketDetailHost
        overlay={overlay}
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
