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
export type FeedCard = components['schemas']['FeedCard'];
export type FeedSummary = components['schemas']['FeedSummary'];
export type FeedSnapshot = components['schemas']['FeedSnapshot'];
export type FeedHistoryPage = components['schemas']['FeedHistoryPage'];
export type ActivityEvent = components['schemas']['ActivityEvent'];
export type FeedSeenRequest = components['schemas']['FeedSeenRequest'];
export type VoiceToken = components['schemas']['VoiceToken'];

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
  /** Called for every `feed` SSE event — always a wholesale replacement (08 §3). */
  onFeed?: (feed: FeedSnapshot) => void;
  /** Called for every `activity` SSE event — ephemeral, never stored (08 §4). */
  onActivity?: (event: ActivityEvent) => void;
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

function isNullableNumber(value: unknown): value is number | null | undefined {
  return value === undefined || value === null || typeof value === 'number';
}

function isFeedCardKind(value: unknown): value is FeedCard['kind'] {
  return value === 'blocker' || value === 'proposal' || value === 'update' || value === 'preview';
}

function isFeedCard(value: unknown): value is FeedCard {
  return (
    isRecord(value) &&
    isFeedCardKind(value.kind) &&
    typeof value.id === 'string' &&
    typeof value.label === 'string' &&
    typeof value.body === 'string' &&
    typeof value.created_at === 'string' &&
    isNullableString(value.ticket_id) &&
    isNullableNumber(value.notification_id) &&
    isNullableString(value.image_url)
  );
}

function isFeedCardArray(value: unknown): value is FeedCard[] {
  return Array.isArray(value) && value.every(isFeedCard);
}

function isFeedSummary(value: unknown): value is FeedSummary {
  return (
    isRecord(value) &&
    typeof value.blocker_count === 'number' &&
    typeof value.update_count === 'number' &&
    typeof value.stream_count === 'number' &&
    typeof value.building === 'number' &&
    typeof value.idle === 'number' &&
    isNullableString(value.last_word_at)
  );
}

function isFeedSnapshot(value: unknown): value is FeedSnapshot {
  return isRecord(value) && isFeedSummary(value.summary) && isFeedCardArray(value.cards);
}

function isFeedHistoryPage(value: unknown): value is FeedHistoryPage {
  return isRecord(value) && isFeedCardArray(value.cards) && typeof value.has_more === 'boolean';
}

function isActivityKind(value: unknown): value is ActivityEvent['kind'] {
  return value === 'thinking' || value === 'toast';
}

function isActivityVerb(value: unknown): value is ActivityEvent['verb'] {
  return (
    value === undefined ||
    value === null ||
    value === 'started' ||
    value === 'nudged' ||
    value === 'finished' ||
    value === 'queued'
  );
}

function isActivityEvent(value: unknown): value is ActivityEvent {
  return (
    isRecord(value) &&
    isActivityKind(value.kind) &&
    (value.on === undefined || value.on === null || typeof value.on === 'boolean') &&
    isActivityVerb(value.verb) &&
    isNullableString(value.ticket_title)
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
    const payload: unknown = JSON.parse(event.data);
    if (isBoard(payload)) {
      handlers.onBoard(payload);
    }
  });

  source.addEventListener('say', (event) => {
    if (!isMessageEvent(event)) {
      return;
    }
    const payload: unknown = JSON.parse(event.data);
    if (isSayEvent(payload)) {
      handlers.onSay(payload);
    }
  });

  source.addEventListener('feed', (event) => {
    if (!isMessageEvent(event)) {
      return;
    }
    const payload: unknown = JSON.parse(event.data);
    if (isFeedSnapshot(payload)) {
      handlers.onFeed?.(payload);
    }
  });

  source.addEventListener('activity', (event) => {
    if (!isMessageEvent(event)) {
      return;
    }
    const payload: unknown = JSON.parse(event.data);
    if (isActivityEvent(payload)) {
      handlers.onActivity?.(payload);
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
  const payload: unknown = await response.json();
  if (!isMessagePostResponse(payload)) {
    throw new Error('postMessage: unexpected response shape');
  }
  return payload;
}

/** `GET /api/feed` — the same absolute snapshot shape as the `feed` SSE event (08 §3). */
export async function fetchFeed(): Promise<FeedSnapshot> {
  const response = await fetch('/api/feed');
  if (!response.ok) {
    // A transient 5xx returns a plain-text body ("read feed"), so surface the
    // status as the error rather than letting `response.json()` throw an opaque
    // parse error — the caller retries/resyncs on any thrown error.
    throw new Error(`fetchFeed: HTTP ${String(response.status)}`);
  }
  const payload: unknown = await response.json();
  if (!isFeedSnapshot(payload)) {
    throw new Error('fetchFeed: unexpected response shape');
  }
  return payload;
}

/** `GET /api/feed/history?before=&limit=` — one older keyset page of retained
 * update/preview cards (08 D2′), newest-first. `before` omitted starts from the
 * newest; the returned `has_more` signals another page remains. */
export async function fetchFeedHistory(before?: number, limit?: number): Promise<FeedHistoryPage> {
  const params = new URLSearchParams();
  if (before !== undefined) {
    params.set('before', String(before));
  }
  if (limit !== undefined) {
    params.set('limit', String(limit));
  }
  const query = params.toString();
  const response = await fetch(query === '' ? '/api/feed/history' : `/api/feed/history?${query}`);
  if (!response.ok) {
    throw new Error(`fetchFeedHistory: HTTP ${String(response.status)}`);
  }
  const payload: unknown = await response.json();
  if (!isFeedHistoryPage(payload)) {
    throw new Error('fetchFeedHistory: unexpected response shape');
  }
  return payload;
}

/** `POST /api/feed/seen` — marks update/preview cards seen up to `lastNotificationId` (08 §3). */
export async function postFeedSeen(lastNotificationId: number): Promise<void> {
  const body: FeedSeenRequest = { last_notification_id: lastNotificationId };
  await fetch('/api/feed/seen', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}

/** `POST /api/dev/reset` — wipe the board, chat, and live agent sandboxes for a
 * fresh agent session. Drives the /debug "Reset session" button; not part of the
 * wire schema. Throws on a non-2xx so the caller can skip the reload. */
export async function postReset(): Promise<void> {
  const response = await fetch('/api/dev/reset', { method: 'POST' });
  if (!response.ok) {
    throw new Error('postReset: reset failed');
  }
}

function isVoiceToken(value: unknown): value is VoiceToken {
  return isRecord(value) && typeof value.token === 'string' && typeof value.expires_at === 'string';
}

/** `POST /api/voice/token` — mint a short-lived AssemblyAI streaming token (09 §2).
 * The client opens the STT WebSocket directly with `token` and refreshes before
 * `expires_at`; the real API key never leaves the backend (02 §2). */
export async function fetchVoiceToken(): Promise<VoiceToken> {
  const response = await fetch('/api/voice/token', { method: 'POST' });
  if (!response.ok) {
    throw new Error('fetchVoiceToken: mint failed');
  }
  const payload: unknown = await response.json();
  if (!isVoiceToken(payload)) {
    throw new Error('fetchVoiceToken: unexpected response shape');
  }
  return payload;
}

/** `POST /api/tickets/{id}/accept` — routes acceptance through the brain like `postMessage` (08 contract). */
export async function acceptTicket(id: string): Promise<MessagePostResponse> {
  const response = await fetch(`/api/tickets/${id}/accept`, { method: 'POST' });
  const payload: unknown = await response.json();
  if (!isMessagePostResponse(payload)) {
    throw new Error('acceptTicket: unexpected response shape');
  }
  return payload;
}
