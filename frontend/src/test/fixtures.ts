// Shared fixtures for the frontend test suite (07 §9 targets + store/transport
// tests). Not a test file itself — plain typed factories built from the wire
// schema (`Ticket`/`Board`), never hand-invented shapes.
import type { Board } from '@/transport/transport';
import type { Ticket } from '@/components/TicketCard';

export interface TicketFixtureInput {
  id: string;
  title: string;
  body: string;
  state: Ticket['state'];
  priority: number;
  createdAt: string;
  updatedAt: string;
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
    created_at: input.createdAt,
    updated_at: input.updatedAt,
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
    ...overrides,
  };
}

/** A blocked reason long enough to exercise the "shown in full, never
 * truncated" requirement (07 §7, §9). */
export const LONG_BLOCKED_REASON =
  'Waiting on a decision from the user about which payment provider to integrate first: ' +
  'Stripe supports the subscription model we need out of the box, but the team previously ' +
  'evaluated Paddle for tax handling in the EU and no final call was recorded in the ticket ' +
  'history, so the worker cannot proceed without a human answering in chat.';
