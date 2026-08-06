// The `/kanban` board view, presentational — the desktop shell wearing columns
// instead of a feed.
//
// Two regions: the projects rail, **unchanged**, and a board of five columns
// (03 §2.1's states). The rail is not a lookalike — it is literally
// `DesktopRail`, the same component `/app` mounts, so switching between the two
// views moves nothing on the left edge and a change to project switching lands
// in both at once.
//
// **Why this exists beside the feed.** `/app` answers "what should I look at
// now" — the brain's curated narration, newest first (08 D1). It deliberately
// cannot answer "where does everything stand", because a ticket can sit in Ready
// for a day without earning a card. This view answers only that second question,
// off the board snapshot, and does nothing else: it is a **read** of the board,
// not a way to drive it.
//
// **There is no drag-and-drop, and that is a product rule rather than a missing
// feature** (07 D5). Every state transition on this board belongs to the brain —
// the pull decides what moves from Ready to Working, an agent decides when it is
// Blocked, the user accepts a proposal. A column a ticket could be dragged into
// would be a second, contradicting source of truth about the same states. Cards
// therefore open the ticket, and everything a person can decide about it is
// inside the sheet that opens — the same sheet, with the same actions, that the
// feed opens (13 D7a: on a desk it is a panel at the right edge).
//
// The register is the desktop shell's (13 §1): the columns hold still, the only
// thing permitted to move on its own is the building mark, and the accent means
// a person is needed — which on this screen is Blocked and nothing else.
import { useCallback, useState, type JSX } from 'react';
import type { Board, ConnectionState, NotificationModeValue } from '@/transport/transport';
import type { WebPushStatus } from '@/stores/use-web-push';
import type { Ticket } from '@/components/TicketCard';
import { TicketDetail, type TicketTextEdit } from '@/components/TicketDetail';
import { TicketDetailTranscript } from '@/components/TicketDetailTranscript';
import { TicketDetailVoiceActions } from '@/components/TicketDetailVoiceActions';
import { DesktopRail } from '@/components/desktop/DesktopRail';
import type { RailProject } from '@/components/desktop/ProjectsRail';
import { kanbanColumns, type KanbanCard } from '@/components/desktop/kanban-board';
import { workingStatusNote } from '@/components/desktop/working-now';
import { useDesktopShellFlag } from '@/components/desktop/use-desktop-layout';
import { useDeepLinkTicket } from '@/components/use-deep-link-ticket';
import { relativeAge } from '@/components/feed-format';
import '@/components/PrimaryScreen.css';
import '@/components/desktop/DesktopScreen.css';
import '@/components/desktop/Kanban.css';

export interface KanbanScreenViewProps {
  /** The rail's rows — handed straight through to the shared rail (13 §5). */
  projects: RailProject[];
  currentProjectId: string | null;
  onSelectProject: (id: string) => void;
  /** The board snapshot the columns are read from; null before the first one
   * lands, which renders five empty columns under the loading line. */
  board: Board | null;
  connectionState: ConnectionState;
  /** True while this project's snapshot is being fetched — the project-switch
   * wait (12 §4.1), including the refresh behind cache-seeded data. */
  loading?: boolean;
  /** Required, unlike the other actions: accepting a shaped proposal is the one
   * decision this board is guaranteed to be asked for, and a shell that could
   * silently omit it would render a proposal column with no way out of it. */
  onAccept: (ticketId: string) => void;
  onDelete?: ((ticketId: string) => void) | undefined;
  onPoke?: ((ticketId: string) => void) | undefined;
  onSetKeepSandbox?: ((ticketId: string, keep: boolean) => void) | undefined;
  onKillSandbox?: ((ticketId: string) => void) | undefined;
  onReassignSandbox?: ((ticketId: string) => void) | undefined;
  onEditText?: ((ticketId: string, patch: TicketTextEdit) => void) | undefined;
  notificationMode?: NotificationModeValue;
  onSelectNotificationMode?: ((mode: NotificationModeValue) => void) | undefined;
  pushStatus?: WebPushStatus | undefined;
  onEnablePush?: (() => void) | undefined;
  onDisablePush?: (() => void) | undefined;
  /** Injected "now" for deterministic relative-age rendering. */
  now?: number;
}

/** The full ticket a card id points at, resolved against the live snapshot.
 * Every bucket is scanned so a ticket that changes state between the click and
 * the render still resolves — on this screen more than anywhere, since the whole
 * board is on display while the sheet is open. */
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

/** A card's accessible name: everything the card shows, as a sentence. The
 * visible row is fragments (a mark, a title, a bare "12m") which read as a list
 * of unrelated words out loud; this is the same information said properly. */
function cardLabel(card: KanbanCard, columnLabel: string, now: number): string {
  const note = card.status === null ? '' : workingStatusNote(card.status);
  const parts = [card.ticket.title, columnLabel.toLowerCase()];
  if (note !== '') {
    parts.push(note);
  }
  const age = relativeAge(card.ticket.state_changed_at, now);
  parts.push(age === 'now' ? 'just now' : `for ${age}`);
  return `Open ticket: ${parts.join(' — ')}`;
}

export function KanbanScreenView({
  projects,
  currentProjectId,
  onSelectProject,
  board,
  connectionState,
  loading = false,
  onAccept,
  onDelete,
  onPoke,
  onSetKeepSandbox,
  onKillSandbox,
  onReassignSandbox,
  onEditText,
  notificationMode = 'blocked',
  onSelectNotificationMode,
  pushStatus,
  onEnablePush,
  onDisablePush,
  now = Date.now(),
}: KanbanScreenViewProps): JSX.Element {
  // Same stamp, same reason, as the feed shell: the ticket sheet portals out of
  // this subtree and has to know it is a side panel here too.
  useDesktopShellFlag();

  const columns = kanbanColumns(board);
  const disconnected = connectionState === 'reconnecting';

  const [openTicketId, setOpenTicketId] = useState<string | null>(null);
  const closeTicket = useCallback((): void => {
    setOpenTicketId(null);
  }, []);
  // A tapped push notification deep-links to a ticket from whichever desktop
  // view happens to be open — being found is not a property of the layout.
  useDeepLinkTicket(setOpenTicketId);
  // A live voice session inside the open sheet, reported up from its voice
  // cluster — the same seam and the same reason as the feed shell's (this view
  // is not a voice consumer either). The rearrangement it drives is the sheet's
  // own, so both desktop views get it from one place.
  const [ticketVoiceActive, setTicketVoiceActive] = useState(false);

  const openTicket = findTicket(board, openTicketId);
  const openAgentStatus =
    openTicket === null
      ? undefined
      : board?.agents.find((agent) => agent.ticket_id === openTicket.id)?.status;

  return (
    // The root wears `data-role="desktop-screen"`, not a role of its own: this
    // IS the desktop shell — the same viewport lock, the same box model, the
    // same bell anchoring at the rail's foot — with a different middle. Every
    // shell-scoped rule in DesktopScreen.css therefore applies for free, and
    // `data-view` is the one thing that differs, which Kanban.css keys the
    // column layout off. A second root role would have meant restating four
    // rules that must never disagree with the feed shell's.
    <div data-role="desktop-screen" data-view="kanban" data-connection-state={connectionState}>
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

      <main data-role="kanban-main">
        {/* The project-switch wait, stated exactly as the feed shell states it
            (12 §4.1) — one faint line and the smallest turning mark, above the
            board rather than inside a column. Unlike the feed there is no
            resting state to withhold: five empty columns are not a claim that
            the project is empty, they are the board's own furniture, so they
            stay up throughout and simply fill in. */}
        {loading && (
          <div data-role="desktop-loading">
            <div data-role="desktop-loading-line" role="status">
              <span data-role="desktop-loading-mark" aria-hidden="true" />
              Loading…
            </div>
          </div>
        )}
        <div role="region" aria-label="Board" data-role="kanban-board">
          {columns.map((column) => (
            <section
              key={column.key}
              data-role="kanban-column"
              data-column={column.key}
              // The count is in the accessible name, not only in the visible
              // numeral beside the heading, so the column announces its size
              // when it is reached rather than after every card is read.
              aria-label={`${column.label}, ${column.cards.length.toString()} tickets`}
            >
              <header data-role="kanban-column-head">
                <h2 data-role="kanban-column-label">{column.label}</h2>
                {/* A count is the one number this shell allows itself. 13 §8
                    rules out badges in the *resting* screen, whose premise is
                    that there is nothing to manage — but the premise here is the
                    opposite: a column's depth is the fact a board view exists to
                    report. It is set in the faintest ink and never tinted, so it
                    reads as a measurement rather than as an alarm. */}
                <span data-role="kanban-column-count" aria-hidden="true">
                  {column.cards.length}
                </span>
              </header>
              {column.cards.length === 0 ? (
                // An empty column keeps its head and says nothing else. No
                // "nothing here" copy: on a board, an empty column is a normal
                // reading of a healthy project, not a state to apologise for.
                <p data-role="kanban-column-empty" aria-hidden="true">
                  —
                </p>
              ) : (
                <ol data-role="kanban-column-list">
                  {column.cards.map((card) => {
                    const note = card.status === null ? '' : workingStatusNote(card.status);
                    return (
                      <li key={card.ticket.id}>
                        <button
                          type="button"
                          data-role="kanban-card"
                          data-ticket-id={card.ticket.id}
                          data-state={card.ticket.state}
                          data-status={card.status ?? undefined}
                          aria-label={cardLabel(card, column.label, now)}
                          onClick={() => {
                            setOpenTicketId(card.ticket.id);
                          }}
                        >
                          <span data-role="kanban-card-title">{card.ticket.title}</span>
                          {/* The blocked reason is the loudest thing a board can
                              carry — it is a question waiting on a person — so
                              it rides on the card rather than being something
                              you have to open the ticket to discover. Clamped:
                              the full text is one click away in the sheet. */}
                          {card.ticket.state === 'blocked' &&
                            card.ticket.blocked_reason != null &&
                            card.ticket.blocked_reason !== '' && (
                              <span data-role="kanban-card-reason">
                                {card.ticket.blocked_reason}
                              </span>
                            )}
                          <span data-role="kanban-card-meta">
                            {/* The SAME mark the phone's ticket list and the
                                desktop working strip use, from the same unscoped
                                rules in PrimaryScreen.css — the ticket's own ink
                                (ember while it is worked, fire while it is
                                blocked), textured by the session: breathing
                                while it builds, flat while idle, hollow once
                                stopped, fire when it has failed. Only tickets
                                with a worker bound to them have one; a Ready
                                ticket showing a session mark would be inventing
                                a session. */}
                            {card.status !== null && (
                              <span
                                data-role="status-dot"
                                data-state={card.ticket.state}
                                data-status={card.status}
                                aria-hidden="true"
                              />
                            )}
                            {note !== '' && (
                              <span data-role="kanban-card-note" aria-hidden="true">
                                {note}
                              </span>
                            )}
                            {/* Time in THIS state (`state_changed_at`), not time
                                since the last edit — on a board the useful
                                question is how long something has been sitting
                                where it is. */}
                            <span data-role="kanban-card-age" aria-hidden="true">
                              {relativeAge(card.ticket.state_changed_at, now)}
                            </span>
                          </span>
                        </button>
                      </li>
                    );
                  })}
                </ol>
              )}
            </section>
          ))}
        </div>
      </main>

      {openTicket !== null && (
        // The same sheet the feed opens, wired the same way, at the same edge
        // (13 D7a). Opening a card here must not be a different act from opening
        // one there — a board view that grew its own detail pane would be a
        // second place to learn, and a second place to keep in step.
        <TicketDetail
          ticket={openTicket}
          surface="primary"
          placement="right"
          agentIdle={openAgentStatus === 'idle'}
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
