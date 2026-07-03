// Transport module tests (07 §4–§5, §8): the only code that knows URLs.
// `openStream` is exercised against a fake `EventSource`; the request/response
// functions against a mocked global `fetch`. The scaffold's bodies all throw
// `not implemented` (or, for `openStream`, never construct an `EventSource`),
// so these fail red until the solution phase wires the real calls.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  fetchBoard,
  fetchMessages,
  postMessage,
  openStream,
  type Board,
  type SayEvent,
  type ConnectionState,
  type StreamHandlers,
} from '@/transport/transport';
import { makeBoard, makeTicket } from '@/test/fixtures';

/** Extracts the request URL as a plain string regardless of which overload of
 * `fetch`'s first argument the implementation used — avoids relying on
 * `Object.prototype.toString` for a `Request` (which stringifies uselessly). */
function urlOf(input: RequestInfo | URL | undefined): string {
  if (input === undefined) {
    return '';
  }
  if (typeof input === 'string') {
    return input;
  }
  if (input instanceof URL) {
    return input.toString();
  }
  return input.url;
}

/** Minimal fake standing in for the browser `EventSource` (jsdom has no real
 * implementation). Captures listeners so a test can simulate the server. */
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  readonly url: string;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private readonly listeners = new Map<string, ((event: MessageEvent) => void)[]>();
  closed = false;

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: (event: MessageEvent) => void): void {
    const existing = this.listeners.get(type) ?? [];
    existing.push(listener);
    this.listeners.set(type, existing);
  }

  close(): void {
    this.closed = true;
  }

  emit(type: string, payload: unknown): void {
    const listeners = this.listeners.get(type) ?? [];
    const event = new MessageEvent(type, { data: JSON.stringify(payload) });
    for (const listener of listeners) {
      listener(event);
    }
  }

  triggerOpen(): void {
    this.onopen?.();
  }

  triggerError(): void {
    this.onerror?.();
  }
}

describe('transport', () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  describe('fetchBoard (GET /api/board)', () => {
    it('fetches the board snapshot from /api/board', async () => {
      const board = makeBoard({
        ready: [
          makeTicket({
            id: 't1',
            title: 'Ready ticket',
            body: 'body',
            state: 'ready',
            priority: 1,
            createdAt: '2026-07-01T00:00:00Z',
            updatedAt: '2026-07-01T00:00:00Z',
          }),
        ],
      });
      const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit): Promise<Response> =>
        Promise.resolve(new Response(JSON.stringify(board))),
      );
      vi.stubGlobal('fetch', fetchMock);

      const result = await fetchBoard();

      expect(fetchMock).toHaveBeenCalledTimes(1);
      const [requestedUrl] = fetchMock.mock.calls[0] ?? [];
      expect(urlOf(requestedUrl)).toContain('/api/board');
      expect(result).toEqual(board);
    });
  });

  describe('fetchMessages (GET /api/messages)', () => {
    it('requests /api/messages with the given limit', async () => {
      const messages = [
        { message_id: 1, role: 'user' as const, text: 'hi', timestamp: '2026-07-01T00:00:00Z' },
      ];
      const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit): Promise<Response> =>
        Promise.resolve(new Response(JSON.stringify(messages))),
      );
      vi.stubGlobal('fetch', fetchMock);

      const result = await fetchMessages(50);

      expect(fetchMock).toHaveBeenCalledTimes(1);
      const [requestedUrl] = fetchMock.mock.calls[0] ?? [];
      const url = urlOf(requestedUrl);
      expect(url).toContain('/api/messages');
      expect(url).toContain('limit=50');
      expect(result).toEqual(messages);
    });

    it('requests /api/messages with no limit query when omitted', async () => {
      const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit): Promise<Response> =>
        Promise.resolve(new Response(JSON.stringify([]))),
      );
      vi.stubGlobal('fetch', fetchMock);

      await fetchMessages();

      const [requestedUrl] = fetchMock.mock.calls[0] ?? [];
      expect(urlOf(requestedUrl)).not.toContain('limit=');
    });
  });

  describe('postMessage (POST /api/message)', () => {
    it('POSTs {text} to /api/message and resolves the 202 body', async () => {
      const response = { event_id: 7, message_id: 42 };
      const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit): Promise<Response> =>
        Promise.resolve(new Response(JSON.stringify(response), { status: 202 })),
      );
      vi.stubGlobal('fetch', fetchMock);

      const result = await postMessage('build the widget');

      expect(fetchMock).toHaveBeenCalledTimes(1);
      const [requestedUrl, init] = fetchMock.mock.calls[0] ?? [];
      expect(urlOf(requestedUrl)).toContain('/api/message');
      expect(init?.method).toBe('POST');
      expect(init?.body).toBe(JSON.stringify({ text: 'build the widget' }));
      expect(result).toEqual(response);
    });
  });

  describe('openStream (GET /api/stream)', () => {
    function collectHandlers(): {
      handlers: StreamHandlers;
      boards: Board[];
      says: SayEvent[];
      states: ConnectionState[];
    } {
      const boards: Board[] = [];
      const says: SayEvent[] = [];
      const states: ConnectionState[] = [];
      const handlers: StreamHandlers = {
        onBoard: (board) => boards.push(board),
        onSay: (event) => says.push(event),
        onConnectionStateChange: (state) => states.push(state),
      };
      return { handlers, boards, says, states };
    }

    it('opens a single EventSource against /api/stream', () => {
      const { handlers } = collectHandlers();

      openStream(handlers);

      expect(FakeEventSource.instances).toHaveLength(1);
      expect(FakeEventSource.instances[0]?.url).toContain('/api/stream');
    });

    it('relays a `board` SSE event to onBoard with the parsed payload', () => {
      const { handlers, boards } = collectHandlers();
      const board = makeBoard();

      openStream(handlers);
      const source = FakeEventSource.instances[0];
      source?.emit('board', board);

      expect(boards).toEqual([board]);
    });

    it('relays a `say` SSE event to onSay with the parsed payload', () => {
      const { handlers, says } = collectHandlers();
      const sayEvent = { message_id: 9, text: 'hello from kiln', at: '2026-07-01T00:00:00Z' };

      openStream(handlers);
      const source = FakeEventSource.instances[0];
      source?.emit('say', sayEvent);

      expect(says).toEqual([sayEvent]);
    });

    it('reports connected on open and reconnecting on error (07 §8)', () => {
      const { handlers, states } = collectHandlers();

      openStream(handlers);
      const source = FakeEventSource.instances[0];
      source?.triggerOpen();
      source?.triggerError();

      expect(states).toEqual(['connected', 'reconnecting']);
    });

    it('close() tears down the underlying EventSource', () => {
      const { handlers } = collectHandlers();

      const connection = openStream(handlers);
      connection.close();

      expect(FakeEventSource.instances[0]?.closed).toBe(true);
    });
  });
});
