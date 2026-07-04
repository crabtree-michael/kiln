// Multiplexes the single app-wide `/api/stream` connection (07 §5 — "one
// thin module", one connection) across however many stores want to observe
// it. Both the board store and the chat store need live `board`/`say`
// events and connection-state changes, but spec 07 §5/§8 and the
// `App.integration.test.tsx` contract both want exactly one `EventSource`
// for the whole app — not one per store. This module is the single call
// site for `transport.openStream`: the first subscriber opens the real
// connection, later subscribers just get fanned events from it, and the
// last one to unsubscribe closes it.
import { openStream } from '@/transport/transport';
import type {
  ActivityEvent,
  Board,
  ConnectionState,
  FeedSnapshot,
  SayEvent,
  StreamConnection,
  StreamHandlers,
} from '@/transport/transport';

let connection: StreamConnection | null = null;
const subscribers = new Set<StreamHandlers>();

function fanOutBoard(board: Board): void {
  for (const subscriber of subscribers) {
    subscriber.onBoard(board);
  }
}

function fanOutSay(event: SayEvent): void {
  for (const subscriber of subscribers) {
    subscriber.onSay(event);
  }
}

function fanOutFeed(feed: FeedSnapshot): void {
  for (const subscriber of subscribers) {
    subscriber.onFeed?.(feed);
  }
}

function fanOutActivity(event: ActivityEvent): void {
  for (const subscriber of subscribers) {
    subscriber.onActivity?.(event);
  }
}

function fanOutConnectionState(state: ConnectionState): void {
  for (const subscriber of subscribers) {
    subscriber.onConnectionStateChange(state);
  }
}

/** Registers `handlers` against the shared stream; returns an unsubscribe function. */
export function subscribeStream(handlers: StreamHandlers): () => void {
  subscribers.add(handlers);
  connection ??= openStream({
    onBoard: fanOutBoard,
    onSay: fanOutSay,
    onFeed: fanOutFeed,
    onActivity: fanOutActivity,
    onConnectionStateChange: fanOutConnectionState,
  });

  return () => {
    subscribers.delete(handlers);
    if (subscribers.size === 0) {
      connection?.close();
      connection = null;
    }
  };
}
