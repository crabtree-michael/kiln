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
export type AgentStatus = components['schemas']['AgentStatus'];
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
export type PushKey = components['schemas']['PushKey'];
export type PushSubscriptionPayload = components['schemas']['PushSubscription'];
export type NotificationMode = components['schemas']['NotificationMode'];
/** The push-notification frequency values, mirroring the wire enum. */
export type NotificationModeValue = NotificationMode['mode'];
export type Me = components['schemas']['Me'];
export type MeProject = components['schemas']['MeProject'];
export type SettingsUpdateRequest = components['schemas']['SettingsUpdateRequest'];
export type ProjectUpdateRequest = components['schemas']['ProjectUpdateRequest'];
export type VerifyResponse = components['schemas']['VerifyResponse'];
export type VerifyCheck = components['schemas']['VerifyCheck'];

type MeUser = components['schemas']['MeUser'];
type MeSettings = components['schemas']['MeSettings'];
type SecretStatus = components['schemas']['SecretStatus'];

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
    typeof value.state_changed_at === 'string' &&
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
  return (
    value === 'blocker' ||
    value === 'proposal' ||
    value === 'update' ||
    value === 'preview' ||
    value === 'poke' ||
    value === 'done'
  );
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
    isNullableString(value.ticket_title) &&
    isNullableString(value.ticket_id)
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

/** `POST /api/feed/{id}/dismiss` — clear (dismiss) one update/preview card for
 * good by its notification id (swipe-to-dismiss, 08 §3). Idempotent server-side;
 * fire-and-forget like `postFeedSeen`, the resulting `feed` snapshot drops the
 * card. */
export async function dismissFeedCard(notificationId: number): Promise<void> {
  const response = await fetch(`/api/feed/${String(notificationId)}/dismiss`, { method: 'POST' });
  if (!response.ok) {
    throw new Error(`dismissFeedCard: HTTP ${String(response.status)}`);
  }
}

/** `POST /api/feed/dismiss-all` — clear (dismiss) every feed notification at
 * once (the header trash affordance, 08 §3). Idempotent server-side; the
 * resulting `feed` snapshot drops them all. */
export async function dismissAllFeedCards(): Promise<void> {
  const response = await fetch('/api/feed/dismiss-all', { method: 'POST' });
  if (!response.ok) {
    throw new Error(`dismissAllFeedCards: HTTP ${String(response.status)}`);
  }
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

function isMeUser(value: unknown): value is MeUser {
  return (
    isRecord(value) &&
    typeof value.github_login === 'string' &&
    typeof value.display_name === 'string' &&
    typeof value.avatar_url === 'string'
  );
}

function isMeProject(value: unknown): value is MeProject {
  return (
    isRecord(value) &&
    typeof value.name === 'string' &&
    typeof value.repo_url === 'string' &&
    typeof value.amika_snapshot === 'string' &&
    typeof value.brain_model === 'string' &&
    typeof value.worker_count === 'number'
  );
}

function isSecretStatus(value: unknown): value is SecretStatus {
  return isRecord(value) && typeof value.set === 'boolean' && typeof value.tail === 'string';
}

function isMeSettings(value: unknown): value is MeSettings {
  return (
    isRecord(value) &&
    isSecretStatus(value.anthropic_api_key) &&
    isSecretStatus(value.amika_api_key) &&
    isSecretStatus(value.github_auth_token) &&
    typeof value.amika_claude_cred_id === 'string'
  );
}

function isMe(value: unknown): value is Me {
  return (
    isRecord(value) &&
    isMeUser(value.user) &&
    (value.project === undefined || isMeProject(value.project)) &&
    isMeSettings(value.settings)
  );
}

function isVerifyCheckName(value: unknown): value is VerifyCheck['name'] {
  return value === 'anthropic' || value === 'amika' || value === 'repo';
}

function isVerifyCheckStatus(value: unknown): value is VerifyCheck['status'] {
  return value === 'ok' || value === 'failed' || value === 'skipped';
}

function isVerifyCheck(value: unknown): value is VerifyCheck {
  return (
    isRecord(value) &&
    isVerifyCheckName(value.name) &&
    isVerifyCheckStatus(value.status) &&
    typeof value.message === 'string'
  );
}

function isVerifyCheckArray(value: unknown): value is VerifyCheck[] {
  return Array.isArray(value) && value.every(isVerifyCheck);
}

function isVerifyResponse(value: unknown): value is VerifyResponse {
  return isRecord(value) && isVerifyCheckArray(value.checks);
}

/** `GET /api/me` — the signed-in user's account view (11 §4). A `401` means no
 * valid session, which is a normal signed-out state, not an error — resolves
 * `null` rather than throwing so callers can branch on it directly. */
export async function fetchMe(): Promise<Me | null> {
  const response = await fetch('/api/me');
  if (response.status === 401) {
    return null;
  }
  if (!response.ok) {
    throw new Error(`fetchMe: HTTP ${String(response.status)}`);
  }
  const payload: unknown = await response.json();
  if (!isMe(payload)) {
    throw new Error('fetchMe: unexpected response shape');
  }
  return payload;
}

function isPushKey(value: unknown): value is PushKey {
  return isRecord(value) && typeof value.key === 'string';
}

/** `GET /api/push/key` — the VAPID public key for pushManager.subscribe (02 §10).
 * Returns `null` when push is not configured on the backend (404), which the
 * caller treats as "notifications unavailable". */
export async function fetchPushKey(): Promise<string | null> {
  const response = await fetch('/api/push/key');
  if (response.status === 404) {
    return null;
  }
  if (!response.ok) {
    throw new Error(`fetchPushKey: HTTP ${String(response.status)}`);
  }
  const payload: unknown = await response.json();
  if (!isPushKey(payload)) {
    throw new Error('fetchPushKey: unexpected response shape');
  }
  return payload.key;
}

/** `POST /api/push/subscribe` — register a browser push subscription (02 §10).
 * The body is the browser `PushSubscription.toJSON()` shape; upsert on endpoint,
 * so a re-subscribe is idempotent. */
export async function postPushSubscription(sub: PushSubscriptionPayload): Promise<void> {
  const response = await fetch('/api/push/subscribe', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(sub),
  });
  if (!response.ok) {
    throw new Error(`postPushSubscription: HTTP ${String(response.status)}`);
  }
}

function isNotificationMode(value: unknown): value is NotificationMode {
  return isRecord(value) && (value.mode === 'all' || value.mode === 'blocked');
}

/** `GET /api/push/mode` — the current push-notification frequency (02 §10). */
export async function fetchNotificationMode(): Promise<NotificationModeValue> {
  const response = await fetch('/api/push/mode');
  if (!response.ok) {
    throw new Error(`fetchNotificationMode: HTTP ${String(response.status)}`);
  }
  const payload: unknown = await response.json();
  if (!isNotificationMode(payload)) {
    throw new Error('fetchNotificationMode: unexpected response shape');
  }
  return payload.mode;
}

/** `PUT /api/push/mode` — set the push-notification frequency (02 §10). Returns
 * the stored mode the server echoes back. */
export async function putNotificationMode(
  mode: NotificationModeValue,
): Promise<NotificationModeValue> {
  const response = await fetch('/api/push/mode', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode }),
  });
  if (!response.ok) {
    throw new Error(`putNotificationMode: HTTP ${String(response.status)}`);
  }
  const payload: unknown = await response.json();
  if (!isNotificationMode(payload)) {
    throw new Error('putNotificationMode: unexpected response shape');
  }
  return payload.mode;
}

/** `PUT /api/settings` — updates config/secrets; empty/omitted fields are
 * left unchanged (write-only secrets, 11 §3 D7). Returns the refreshed `Me`. */
export async function putSettings(body: SettingsUpdateRequest): Promise<Me> {
  const response = await fetch('/api/settings', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    throw new Error(`putSettings: HTTP ${String(response.status)}`);
  }
  const payload: unknown = await response.json();
  if (!isMe(payload)) {
    throw new Error('putSettings: unexpected response shape');
  }
  return payload;
}

/** `PUT /api/project` — creates/updates the user's single project. Returns the
 * refreshed `Me`. */
export async function putProject(body: ProjectUpdateRequest): Promise<Me> {
  const response = await fetch('/api/project', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    throw new Error(`putProject: HTTP ${String(response.status)}`);
  }
  const payload: unknown = await response.json();
  if (!isMe(payload)) {
    throw new Error('putProject: unexpected response shape');
  }
  return payload;
}

/** `POST /api/settings/verify` — runs the per-check connectivity verification
 * (anthropic/amika/repo); unconfigured checks report status "skipped". */
export async function postVerify(): Promise<VerifyResponse> {
  const response = await fetch('/api/settings/verify', { method: 'POST' });
  if (!response.ok) {
    throw new Error(`postVerify: HTTP ${String(response.status)}`);
  }
  const payload: unknown = await response.json();
  if (!isVerifyResponse(payload)) {
    throw new Error('postVerify: unexpected response shape');
  }
  return payload;
}

/** `POST /auth/logout` — ends the signed-in session. Not part of the wire
 * schema (like `postReset`); the caller re-fetches `/api/me` afterward to
 * observe the resulting signed-out state. */
export async function postLogout(): Promise<void> {
  const response = await fetch('/auth/logout', { method: 'POST' });
  if (!response.ok) {
    throw new Error(`postLogout: HTTP ${String(response.status)}`);
  }
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
