// Shared fixtures for the frontend test suite (07 §9 targets + store/transport
// tests). Not a test file itself — plain typed factories built from the wire
// schema (`Ticket`/`Board`), never hand-invented shapes.
import type {
  ActivityEvent,
  AgentStatus,
  Board,
  FeedCard,
  FeedSnapshot,
  FeedSummary,
} from '@/transport/transport';
import type { Ticket } from '@/components/TicketCard';

export interface TicketFixtureInput {
  id: string;
  title: string;
  body: string;
  state: Ticket['state'];
  priority: number;
  createdAt: string;
  updatedAt: string;
  /** When the ticket entered its current status; defaults to `updatedAt` when
   * omitted (fine for the many fixtures that don't exercise time-in-status). */
  statusChangedAt?: string;
  approvalRequested?: boolean;
  blockedReason?: string;
  readyAt?: string;
}

/** Builds a `Ticket` from the wire schema, adding optional fields only when
 * supplied (keeps `exactOptionalPropertyTypes` happy — never assigns
 * `undefined` to an optional key). */
export function makeTicket(input: TicketFixtureInput): Ticket {
  const base: Ticket = {
    id: input.id,
    title: input.title,
    body: input.body,
    state: input.state,
    priority: input.priority,
    approval_requested: input.approvalRequested ?? false,
    created_at: input.createdAt,
    updated_at: input.updatedAt,
    state_changed_at: input.statusChangedAt ?? input.updatedAt,
  };
  const withBlocked =
    input.blockedReason !== undefined ? { ...base, blocked_reason: input.blockedReason } : base;
  const withReady =
    input.readyAt !== undefined ? { ...withBlocked, ready_at: input.readyAt } : withBlocked;
  return withReady;
}

export function makeBoard(overrides: Partial<Board> = {}): Board {
  return {
    shaping: [],
    ready: [],
    blocked: [],
    working: [],
    done: [],
    worker_total: 4,
    worker_free: 4,
    agents: [],
    ...overrides,
  };
}

/** Builds one `AgentStatus` join entry (amended 2026-07-05) — a live worker's
 * real session state keyed to a ticket, for asserting the Streams view. */
export function makeAgentStatus(
  ticketId: string,
  status: AgentStatus['status'],
  workerId = `w-${ticketId}`,
): AgentStatus {
  return { worker_id: workerId, ticket_id: ticketId, status };
}

export interface FeedCardFixtureInput {
  kind: FeedCard['kind'];
  id: string;
  label: string;
  body: string;
  createdAt: string;
  ticketId?: string;
  notificationId?: number;
  imageUrl?: string;
}

/** Builds a `FeedCard` from the wire schema, adding optional fields only when
 * supplied (mirrors `makeTicket` — never assigns `undefined` to an optional key). */
export function makeFeedCard(input: FeedCardFixtureInput): FeedCard {
  const base: FeedCard = {
    kind: input.kind,
    id: input.id,
    label: input.label,
    body: input.body,
    created_at: input.createdAt,
  };
  const withTicket = input.ticketId !== undefined ? { ...base, ticket_id: input.ticketId } : base;
  const withNotification =
    input.notificationId !== undefined
      ? { ...withTicket, notification_id: input.notificationId }
      : withTicket;
  const withImage =
    input.imageUrl !== undefined
      ? { ...withNotification, image_url: input.imageUrl }
      : withNotification;
  return withImage;
}

export function makeFeedSummary(overrides: Partial<FeedSummary> = {}): FeedSummary {
  return {
    blocker_count: 0,
    update_count: 0,
    stream_count: 0,
    building: 0,
    idle: 0,
    ...overrides,
  };
}

export interface FeedSnapshotFixtureInput {
  summary?: Partial<FeedSummary>;
  cards?: FeedCard[];
  hasMoreHistory?: boolean;
}

export function makeFeedSnapshot(input: FeedSnapshotFixtureInput = {}): FeedSnapshot {
  return {
    summary: makeFeedSummary(input.summary),
    cards: input.cards ?? [],
    has_more_history: input.hasMoreHistory ?? false,
  };
}

export interface ActivityEventFixtureInput {
  kind: ActivityEvent['kind'];
  on?: boolean;
  verb?: NonNullable<ActivityEvent['verb']>;
  ticketTitle?: string;
  ticketId?: string;
}

/** Builds an `ActivityEvent` from the wire schema, adding optional fields only
 * when supplied (mirrors `makeTicket`/`makeFeedCard`). */
export function makeActivityEvent(input: ActivityEventFixtureInput): ActivityEvent {
  const base: ActivityEvent = { kind: input.kind };
  const withOn = input.on !== undefined ? { ...base, on: input.on } : base;
  const withVerb = input.verb !== undefined ? { ...withOn, verb: input.verb } : withOn;
  const withTitle =
    input.ticketTitle !== undefined ? { ...withVerb, ticket_title: input.ticketTitle } : withVerb;
  const withId =
    input.ticketId !== undefined ? { ...withTitle, ticket_id: input.ticketId } : withTitle;
  return withId;
}

/** A blocked reason long enough to exercise the "shown in full, never
 * truncated" requirement (07 §7, §9). */
export const LONG_BLOCKED_REASON =
  'Waiting on a decision from the user about which payment provider to integrate first: ' +
  'Stripe supports the subscription model we need out of the box, but the team previously ' +
  'evaluated Paddle for tax handling in the EU and no final call was recorded in the ticket ' +
  'history, so the worker cannot proceed without a human answering in chat.';
