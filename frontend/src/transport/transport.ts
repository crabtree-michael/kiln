// The one module that knows URLs (07 §5). Everything else — stores, components
// — talks to the backend only through the functions exported here: `fetch` for
// the request/response endpoints, and a thin `EventSource` wrapper for the
// live `board`/`say` stream (07 §4).
//
// `fetch`'s `Response.json()` and `EventSource`'s `MessageEvent.data` are both
// typed loosely by lib.dom (`any`/generic `T = any`); the escape-hatch ban
// (02 §4b: no `any`, no `as`) means we narrow them back to the generated wire
// types with small runtime type guards (`isBoard`, `isMessage`, ...) rather
// than casting. The guards are intentionally shallow — this is a thin client
// trusting its own backend, not a public API boundary — but they are enough
// to keep every value flowing through here statically typed, never `any`.
import type { components } from '@/schema/generated';

export type Ticket = components['schemas']['Ticket'];
export type Board = components['schemas']['Board'];
export type Message = components['schemas']['Message'];
export type MessagePostResponse = components['schemas']['MessagePostResponse'];
export type SayEvent = components['schemas']['SayEvent'];

/**
 * Stream connection state (07 §8): `EventSource` retries natively, so this is
 * purely a display concern — the board dims while reconnecting but stays
 * rendered (stale-but-visible beats blank).
 */
export type ConnectionState = 'connecting' | 'connected' | 'reconnecting';

export interface StreamHandlers {
  /** Called for every `board` SSE event — always a wholesale replacement (04 D7). */
  onBoard: (board: Board) => void;
  /** Called for every `say` SSE event — one per brain `say` (07 §3). */
  onSay: (event: SayEvent) => void;
  /** Called whenever the underlying connection's state changes (07 §8). */
  onConnectionStateChange: (state: ConnectionState) => void;
}

export interface StreamConnection {
  /** Tears down the underlying `EventSource`. */
  close: () => void;
}

// --- Runtime shape guards (narrow `unknown` -> a generated wire type) ---

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function isNullableString(value: unknown): value is string | null | undefined {
  return value === undefined || value === null || typeof value === 'string';
}

function isTicketState(value: unknown): value is Ticket['state'] {
  return (
    value === 'shaping' ||
    value === 'ready' ||
    value === 'working' ||
    value === 'blocked' ||
    value === 'done'
  );
}

function isTicket(value: unknown): value is Ticket {
  return (
    isRecord(value) &&
    typeof value.id === 'string' &&
    typeof value.title === 'string' &&
    typeof value.body === 'string' &&
    isTicketState(value.state) &&
    typeof value.priority === 'number' &&
    typeof value.created_at === 'string' &&
    typeof value.updated_at === 'string' &&
    isNullableString(value.blocked_reason) &&
    isNullableString(value.ready_at)
  );
}

function isTicketArray(value: unknown): value is Ticket[] {
  return Array.isArray(value) && value.every(isTicket);
}

function isBoard(value: unknown): value is Board {
  return (
    isRecord(value) &&
    isTicketArray(value.shaping) &&
    isTicketArray(value.ready) &&
    isTicketArray(value.blocked) &&
    isTicketArray(value.working) &&
    isTicketArray(value.done) &&
    typeof value.worker_total === 'number' &&
    typeof value.worker_free === 'number'
  );
}

function isMessageRole(value: unknown): value is Message['role'] {
  return value === 'user' || value === 'kiln';
}

function isMessage(value: unknown): value is Message {
  return (
    isRecord(value) &&
    typeof value.message_id === 'number' &&
    isMessageRole(value.role) &&
    typeof value.text === 'string' &&
    typeof value.timestamp === 'string'
  );
}

function isMessageArray(value: unknown): value is Message[] {
  return Array.isArray(value) && value.every(isMessage);
}

function isMessagePostResponse(value: unknown): value is MessagePostResponse {
  return (
    isRecord(value) && typeof value.event_id === 'number' && typeof value.message_id === 'number'
  );
}

function isSayEvent(value: unknown): value is SayEvent {
  return (
    isRecord(value) &&
    typeof value.message_id === 'number' &&
    typeof value.text === 'string' &&
    typeof value.at === 'string'
  );
}

function isMessageEvent(event: Event): event is MessageEvent<string> {
  return event instanceof MessageEvent && typeof event.data === 'string';
}

/**
 * Opens the `GET /api/stream` connection and wires the named `board`/`say`
 * SSE events to `handlers`. Reconnection is `EventSource`'s native retry
 * (07 §5, §8) — this function does not implement its own backoff.
 */
export function openStream(handlers: StreamHandlers): StreamConnection {
  const source = new EventSource('/api/stream');

  source.addEventListener('board', (event) => {
    if (!isMessageEvent(event)) {
      return;
    }
    let payload: unknown;
    try {
      payload = JSON.parse(event.data);
    } catch {
      return;
    }
    if (isBoard(payload)) {
      handlers.onBoard(payload);
    }
  });

  source.addEventListener('say', (event) => {
    if (!isMessageEvent(event)) {
      return;
    }
    let payload: unknown;
    try {
      payload = JSON.parse(event.data);
    } catch {
      return;
    }
    if (isSayEvent(payload)) {
      handlers.onSay(payload);
    }
  });

  source.onopen = () => {
    handlers.onConnectionStateChange('connected');
  };
  source.onerror = () => {
    handlers.onConnectionStateChange('reconnecting');
  };

  return {
    close: () => {
      source.close();
    },
  };
}

/** `GET /api/board` — the same absolute snapshot shape as the `board` SSE event. */
export async function fetchBoard(): Promise<Board> {
  const response = await fetch('/api/board');
  if (!response.ok) {
    throw new Error(`fetchBoard: HTTP ${String(response.status)}`);
  }
  const payload: unknown = await response.json();
  if (!isBoard(payload)) {
    throw new Error('fetchBoard: unexpected response shape');
  }
  return payload;
}

/** `GET /api/messages?limit=` — most-recent `limit` transcript rows, oldest-first. */
export async function fetchMessages(limit?: number): Promise<Message[]> {
  const url = limit === undefined ? '/api/messages' : `/api/messages?limit=${String(limit)}`;
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error(`fetchMessages: HTTP ${String(response.status)}`);
  }
  const payload: unknown = await response.json();
  if (!isMessageArray(payload)) {
    throw new Error('fetchMessages: unexpected response shape');
  }
  return payload;
}

/** `POST /api/message` — appends the user row + enqueues `human.message`, in one transaction. */
export async function postMessage(text: string): Promise<MessagePostResponse> {
  const response = await fetch('/api/message', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text }),
  });
  if (!response.ok) {
    throw new Error(`postMessage: HTTP ${String(response.status)}`);
  }
  const payload: unknown = await response.json();
  if (!isMessagePostResponse(payload)) {
    throw new Error('postMessage: unexpected response shape');
  }
  return payload;
}
