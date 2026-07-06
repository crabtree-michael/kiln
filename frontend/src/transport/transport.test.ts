// Transport module tests (07 §4–§5, §8): the only code that knows URLs.
// `openStream` is exercised against a fake `EventSource`; the request/response
// functions against a mocked global `fetch`. The scaffold's bodies all throw
// `not implemented` (or, for `openStream`, never construct an `EventSource`),
// so these fail red until the solution phase wires the real calls.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  fetchBoard,
  fetchFeed,
  fetchMessages,
  postMessage,
  openStream,
  fetchMe,
  putSettings,
  putProject,
  postVerify,
  postLogout,
  type Board,
  type FeedSnapshot,
  type SayEvent,
  type ConnectionState,
  type StreamHandlers,
  type Me,
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

  describe('fetchFeed (GET /api/feed)', () => {
    // Regression: the steward posts a `poke` feed card (mechanical stall nudge).
    // `poke` is a valid FeedCard kind in the wire schema, but the response-shape
    // guard once omitted it, so a single poke card made `fetchFeed` reject the
    // whole snapshot as malformed — the feed then stayed null and the primary
    // screen was stuck on its empty "0 building" state while /api/board (which
    // never carries a poke) kept the debug view working. The guard must accept
    // every schema kind, `poke` included.
    it('accepts a snapshot containing a poke card', async () => {
      const snapshot: FeedSnapshot = {
        summary: {
          blocker_count: 0,
          update_count: 0,
          stream_count: 1,
          building: 1,
          idle: 0,
        },
        cards: [
          {
            kind: 'poke',
            id: 'update:1',
            label: 'Stalled ticket',
            body: '',
            created_at: '2026-07-06T00:00:00Z',
            notification_id: 1,
          },
        ],
        has_more_history: false,
      };
      const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit): Promise<Response> =>
        Promise.resolve(new Response(JSON.stringify(snapshot))),
      );
      vi.stubGlobal('fetch', fetchMock);

      const result = await fetchFeed();

      const [requestedUrl] = fetchMock.mock.calls[0] ?? [];
      expect(urlOf(requestedUrl)).toContain('/api/feed');
      expect(result).toEqual(snapshot);
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

  function makeMe(): Me {
    return {
      user: {
        github_login: 'octocat',
        display_name: 'Octocat',
        avatar_url: 'https://example.com/a.png',
      },
      settings: {
        anthropic_api_key: { set: false, tail: '' },
        amika_api_key: { set: false, tail: '' },
        github_auth_token: { set: true, tail: 'abcd' },
        amika_claude_cred_id: '',
      },
    };
  }

  describe('fetchMe (GET /api/me)', () => {
    it('resolves the account view on 200', async () => {
      const me = makeMe();
      const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit): Promise<Response> =>
        Promise.resolve(new Response(JSON.stringify(me))),
      );
      vi.stubGlobal('fetch', fetchMock);

      const result = await fetchMe();

      const [requestedUrl] = fetchMock.mock.calls[0] ?? [];
      expect(urlOf(requestedUrl)).toContain('/api/me');
      expect(result).toEqual(me);
    });

    it('resolves null on 401 rather than throwing (no valid session)', async () => {
      const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit): Promise<Response> =>
        Promise.resolve(new Response(null, { status: 401 })),
      );
      vi.stubGlobal('fetch', fetchMock);

      const result = await fetchMe();

      expect(result).toBeNull();
    });

    it('throws on an unexpected response shape', async () => {
      const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit): Promise<Response> =>
        Promise.resolve(new Response(JSON.stringify({ nope: true }))),
      );
      vi.stubGlobal('fetch', fetchMock);

      await expect(fetchMe()).rejects.toThrow('unexpected response shape');
    });
  });

  describe('putSettings (PUT /api/settings)', () => {
    it('PUTs the body to /api/settings and resolves the refreshed Me', async () => {
      const me = makeMe();
      const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit): Promise<Response> =>
        Promise.resolve(new Response(JSON.stringify(me))),
      );
      vi.stubGlobal('fetch', fetchMock);

      const result = await putSettings({ anthropic_api_key: 'sk-new' });

      const [requestedUrl, init] = fetchMock.mock.calls[0] ?? [];
      expect(urlOf(requestedUrl)).toContain('/api/settings');
      expect(init?.method).toBe('PUT');
      expect(init?.body).toBe(JSON.stringify({ anthropic_api_key: 'sk-new' }));
      expect(result).toEqual(me);
    });

    it('throws on a non-2xx response', async () => {
      const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit): Promise<Response> =>
        Promise.resolve(new Response(null, { status: 400 })),
      );
      vi.stubGlobal('fetch', fetchMock);

      await expect(putSettings({})).rejects.toThrow('HTTP 400');
    });
  });

  describe('putProject (PUT /api/project)', () => {
    it('PUTs the body to /api/project and resolves the refreshed Me', async () => {
      const me = makeMe();
      const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit): Promise<Response> =>
        Promise.resolve(new Response(JSON.stringify(me))),
      );
      vi.stubGlobal('fetch', fetchMock);

      const result = await putProject({ name: 'proj', repo_url: 'https://github.com/a/b' });

      const [requestedUrl, init] = fetchMock.mock.calls[0] ?? [];
      expect(urlOf(requestedUrl)).toContain('/api/project');
      expect(init?.method).toBe('PUT');
      expect(result).toEqual(me);
    });
  });

  describe('postVerify (POST /api/settings/verify)', () => {
    it('POSTs to /api/settings/verify and resolves the checks', async () => {
      const response = {
        checks: [{ name: 'anthropic' as const, status: 'ok' as const, message: 'reachable' }],
      };
      const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit): Promise<Response> =>
        Promise.resolve(new Response(JSON.stringify(response))),
      );
      vi.stubGlobal('fetch', fetchMock);

      const result = await postVerify();

      const [requestedUrl, init] = fetchMock.mock.calls[0] ?? [];
      expect(urlOf(requestedUrl)).toContain('/api/settings/verify');
      expect(init?.method).toBe('POST');
      expect(result).toEqual(response);
    });
  });

  describe('postLogout (POST /auth/logout)', () => {
    it('POSTs to /auth/logout', async () => {
      const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit): Promise<Response> =>
        Promise.resolve(new Response(null)),
      );
      vi.stubGlobal('fetch', fetchMock);

      await postLogout();

      const [requestedUrl, init] = fetchMock.mock.calls[0] ?? [];
      expect(urlOf(requestedUrl)).toContain('/auth/logout');
      expect(init?.method).toBe('POST');
    });

    it('throws on a non-2xx response', async () => {
      const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit): Promise<Response> =>
        Promise.resolve(new Response(null, { status: 500 })),
      );
      vi.stubGlobal('fetch', fetchMock);

      await expect(postLogout()).rejects.toThrow('HTTP 500');
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
