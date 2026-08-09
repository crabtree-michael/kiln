// The harness the layout checks run on: the REAL client, in a REAL browser, with
// every `/api` call stubbed and nothing else faked.
//
// Why this exists at all: jsdom performs no layout, so the unit gate can see
// which elements render and never where they end up. The repo's answer used to
// be prose in a skill doc plus tests that asserted the TEXT of the stylesheets —
// neither of which can tell a rule that is present and correct from a rule that
// is present and wrong. The "Show earlier" control overlapping the toast band
// was fixed five times with those tests green throughout. These specs measure
// boxes and ask the browser what actually paints at a point, which is the thing
// that was broken.
//
// No stack: the client is served by its own dev server (see
// playwright.layout.config.ts) and every request under /api is fulfilled from
// the fixtures below. That is what makes this cheap enough to sit in `make
// check` alongside the unit tests, unlike the e2e suite next door (../tests),
// which drives a live backend and a real LLM.
import type { Page, WebSocketRoute } from '@playwright/test';
import { expect } from '@playwright/test';

/** A rectangle in viewport coordinates — what every assertion here is about. */
export interface Box {
  top: number;
  right: number;
  bottom: number;
  left: number;
  width: number;
  height: number;
}

/** Which band, if any, is standing in the feed's bottom reserve.
 *
 * `'toast'` is the SHORTEST band the app can show — one board toast, no `say` —
 * which is the case where the "Show earlier" control, dropped into the band,
 * comes closest to poking out over its top edge. */
export type Band = 'none' | 'toast' | 'say';

export interface ShellOptions {
  /** Route to open. The feed shell is `/app`; the board view is `/kanban`. */
  route?: string;
  band?: Band;
  /** The floating "Kiln is thinking…" chip — the reserve's other occupant, and
   * the one with no fill to hide anything behind it. */
  thinking?: boolean;
  /** Whether the feed offers history, i.e. whether "Show earlier" renders. */
  hasEarlier?: boolean;
  /** How many update cards the feed carries. Zero is the resting state. */
  cards?: number;
  /** Give one ticket a long body, so the detail sheet caps at its max height. */
  longTicketBody?: boolean;
  /** What "loaded" means for this route. Defaults to either feed shell; the
   * settings page and the board view mount neither. */
  ready?: string;
  /** Report the user as having no project, which is what `/dashboard` switches
   * on to mount the guided setup flow instead of the settings page (12 §4.1). */
  noProject?: boolean;
}

const PROJECT = {
  id: 'p1',
  name: 'kiln',
  repo_url: 'https://github.com/acme/kiln',
  agent_provider: 'mock',
  amika_snapshot: '',
  worker_count: 3,
  merge_gate_mode: 'main',
  amika_secrets: [],
};

const ME = {
  user: { github_login: 'amika', display_name: 'Amika', avatar_url: '' },
  projects: [PROJECT],
  settings: {
    anthropic_api_key: { set: true, tail: 'abcd' },
    amika_api_key: { set: true, tail: 'wxyz' },
    devin_api_key: { set: false, tail: '' },
    github_auth_token: { set: true, tail: '1234' },
    github_connection: {
      status: 'connected',
      login: 'amika',
      installation_id: 4242,
      configure_url: 'https://github.com/settings/installations/4242',
    },
    amika_claude_cred_id: '',
  },
};

/** A body long enough to cap the detail sheet at its max height, so the sheet's
 * flex-shrink distribution is actually exercised rather than assumed. */
const LONG_BODY = Array.from(
  { length: 40 },
  (_, i) =>
    `Paragraph ${String(i + 1)}. The retry cap wants a second pass before the ` +
    `release goes out, and the backoff table needs a row for the 5xx case.`,
).join('\n\n');

function ticket(
  id: string,
  title: string,
  state: string,
  extra: Record<string, unknown> = {},
): Record<string, unknown> {
  return {
    id,
    title,
    body: 'Rate-limit the webhook retry: back off on 5xx, cap at five attempts.',
    state,
    priority: 1,
    approval_requested: false,
    keep_sandbox: false,
    // Required by the wire contract (0013). Present even though no layout spec
    // exercises dependencies: this fixture stands in for a real server response,
    // and a missing required field is a shape the client never has to handle —
    // leaving it out made every ticket-sheet spec fail at the sheet's first
    // read of it, which reads as a layout regression rather than a stale double.
    depends_on: [],
    unmet_dependencies: 0,
    waiting_on_dependencies: false,
    created_at: '2026-08-04T09:00:00Z',
    updated_at: '2026-08-04T11:00:00Z',
    state_changed_at: '2026-08-04T11:00:00Z',
    ...extra,
  };
}

function board(options: ShellOptions): Record<string, unknown> {
  const shaping = ticket(
    't3',
    'Rate-limit the webhook retry',
    'shaping',
    options.longTicketBody === true ? { body: LONG_BODY, approval_requested: true } : {},
  );
  return {
    shaping: [shaping],
    ready: [ticket('t5', 'Backfill the search index', 'ready')],
    blocked: [ticket('t1', 'auth refresh', 'blocked', { blocked_reason: 'Needs a decision.' })],
    working: [ticket('t2', 'poller', 'working')],
    done: [],
    worker_total: 3,
    worker_free: 1,
    agents: [{ worker_id: 'w1', ticket_id: 't2', status: 'building' }],
    alerts: [],
  };
}

function feed(options: ShellOptions): Record<string, unknown> {
  const count = options.cards ?? 6;
  const cards: Record<string, unknown>[] = [];
  // A proposal card first: its clamped body is the one card that opens the
  // ticket detail sheet on tap (08 §5), which is how the detail specs get in.
  cards.push({
    kind: 'proposal',
    id: 'proposal:t3',
    label: 'Rate-limit the webhook retry',
    body: 'Back off on 5xx and cap at five attempts. Worth a look before it lands.',
    ticket_id: 't3',
    created_at: '2026-08-04T11:05:00Z',
  });
  for (let n = 1; n <= count; n += 1) {
    cards.push({
      kind: 'update',
      id: `c${String(n)}`,
      label: `poller ${String(n)}`,
      body: 'Landed the retry and backed it with a table test.',
      created_at: '2026-08-04T11:05:00Z',
      notification_id: 40 + n,
    });
  }
  return {
    summary: {
      blocker_count: 1,
      update_count: 1,
      stream_count: 1,
      building: 1,
      idle: 1,
      last_word_at: '2026-08-04T11:52:00Z',
      last_seen_notification_id: 0,
    },
    has_more_history: options.hasEarlier ?? true,
    cards,
  };
}

/** The event stream the shell opens on boot, as one canned SSE body.
 *
 * `retry:` is the first field on purpose. `route.fulfill` cannot hold a
 * connection open, so the response ends as soon as it is written and EventSource
 * would reconnect (default ~3s) and replay these events over and over — a band
 * that grows while the spec is measuring it. An hour's retry interval makes the
 * canned events land exactly once. */
function streamBody(options: ShellOptions): string {
  const events: string[] = ['retry: 3600000\n\n'];
  if (options.thinking === true) {
    events.push(`event: activity\ndata: ${JSON.stringify({ kind: 'thinking', on: true })}\n\n`);
  }
  if (options.band === 'say') {
    events.push(
      `event: say\ndata: ${JSON.stringify({
        message_id: 1,
        // Deliberately longer than one desk line: an uncapped pill is only
        // visibly wrong once its text has more width to claim than the field it
        // is answering, so a short utterance would let that regression through.
        text:
          'Rate-limiting the webhook retry now — backing off on 5xx, capped at five ' +
          'attempts, with the backoff table getting a row for the 429 case as well so ' +
          'the two paths stop disagreeing about what a retryable failure is.',
        at: '2026-08-04T11:55:00Z',
      })}\n\n`,
    );
  }
  if (options.band === 'say' || options.band === 'toast') {
    events.push(
      `event: activity\ndata: ${JSON.stringify({
        kind: 'toast',
        verb: 'started',
        ticket_title: 'auth refresh',
        ticket_id: 't1',
      })}\n\n`,
    );
  }
  return events.join('');
}

/**
 * Stub every `/api` call, open the shell, and wait until the state the spec
 * asked for is actually on screen.
 *
 * The viewport is the caller's: `test.use({ viewport })` at the describe level
 * is what decides which shell mounts (the switch is `useIsDesktop`, at 1024px).
 */
export async function mountShell(page: Page, options: ShellOptions = {}): Promise<void> {
  await stubSpeech(page);
  await page.route('**/api/**', async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith('/voice/token')) {
      // A well-formed token, so the client gets past `fetchVoiceToken` and opens
      // its socket — which `stubSpeech` above has already intercepted, so the
      // value is never presented to anyone.
      //
      // Twenty minutes out, NOT some far-future date: the store schedules its
      // proactive refresh (09 §5) as `setTimeout(expiry - now - buffer)`, and a
      // delay past the 32-bit millisecond ceiling wraps to ~immediately. A 2099
      // expiry therefore tears the stream down mid-setup — the AudioContext
      // closes while `addModule` is still fetching, which surfaces as a bare
      // AbortError from the worklet and a mic stuck in Retry.
      return route.fulfill({
        json: {
          token: 'layout-gate',
          expires_at: new Date(Date.now() + 20 * 60 * 1000).toISOString(),
        },
      });
    }
    if (url.pathname.endsWith('/me')) {
      return route.fulfill({
        json: options.noProject === true ? { ...ME, projects: [] } : ME,
      });
    }
    if (url.pathname.endsWith('/board')) {
      return route.fulfill({ json: board(options) });
    }
    if (url.pathname.endsWith('/feed')) {
      return route.fulfill({ json: feed(options) });
    }
    if (url.pathname.endsWith('/activity')) {
      return route.fulfill({ json: { thinking: options.thinking === true } });
    }
    if (url.pathname.includes('/stream')) {
      return route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        body: streamBody(options),
      });
    }
    return route.fulfill({ json: {} });
  });

  await page.goto(options.route ?? '/app');
  await page.waitForSelector(
    options.ready ?? "[data-role='primary-screen'], [data-role='desktop-screen']",
  );
  if (options.hasEarlier !== false) {
    await page.waitForSelector("[data-role='feed-show-earlier']");
  }
  if (options.band === 'toast' || options.band === 'say') {
    await page.waitForSelector("[data-role='toast-stack']");
  }
  if (options.thinking === true) {
    await page.waitForSelector("[data-role='thinking-indicator']");
  }
  await settle(page);
}

/** The intercepted STT socket, per page — how `speak` reaches the client with a
 * transcript after `mountShell` has installed the route. */
const speechSockets = new WeakMap<Page, WebSocketRoute>();

/** A stand-in for the PCM downsample worklet, as an inline module.
 *
 * Vite's DEV server cannot serve `pcm-worklet.ts?worker&url` as an AudioWorklet
 * module — it hands back an ES module with the HMR preamble, and a worklet global
 * scope has no import machinery, so `addModule` rejects with a bare `AbortError`
 * and the store lands in Retry with no mic. (A production build inlines the
 * worklet, which is why the app itself is fine; this gate runs the dev server.)
 * Registering the same processor NAME with an empty `process` keeps the real
 * audio graph, the real client and the real store — only the DSP is absent, and
 * nothing here is about PCM. The name must match `PCM_WORKLET_NAME` in
 * assemblyai-client.ts.
 *
 * Handed over as a `data:` URL, which is the one form that reliably survives the
 * trip: a worklet fetches its module in its own realm, so `page.route` never sees
 * the request (it reaches the dev server, comes back as the SPA's index.html, and
 * fails to parse — as the same opaque AbortError), and a blob URL minted in the
 * page races its own registration and fails the same way. A data URL carries the
 * source with it and needs neither. */
const WORKLET_STUB =
  "registerProcessor('pcm16-downsample', class extends AudioWorkletProcessor {" +
  ' process() { return true; } });';

/**
 * Everything the client needs to believe it has a mic and a provider, minus the
 * mic and the provider.
 *
 * The dock's speaking arrangement — send and × flanking the mic — is otherwise
 * unreachable in this gate: it needs a live transcript, and a transcript needs a
 * mic, an audio graph, a minted token and a provider socket. The mic is
 * Chromium's fake capture device (see playwright.layout.config.ts), the token is
 * stubbed with the rest of `/api`, the worklet is swapped for the stub above, and
 * the socket is intercepted here. Nothing is proxied to AssemblyAI — the route is
 * fully mocked, so no key, no network and no billing.
 */
async function stubSpeech(page: Page): Promise<void> {
  await page.addInitScript((source: string) => {
    const load = AudioWorklet.prototype.addModule;
    AudioWorklet.prototype.addModule = function stubbed(this: AudioWorklet): Promise<void> {
      return load.call(this, `data:application/javascript,${encodeURIComponent(source)}`);
    };
  }, WORKLET_STUB);
  await page.routeWebSocket(/streaming\.assemblyai\.com/, (ws) => {
    speechSockets.set(page, ws);
    // The PCM frames the worklet streams up, discarded: nothing here transcribes
    // audio, and `speak` supplies the words directly.
    ws.onMessage(() => {
      /* the mic's own audio, dropped on the floor */
    });
    // `Begin` is what the client reads as "the socket is up" — it clears the
    // mic's connecting spinner, which would otherwise sit over the orb and
    // change what the specs are measuring.
    ws.send(JSON.stringify({ type: 'Begin', id: 'layout-gate', expires_at: 4102444800 }));
  });
}

/**
 * Turn the mic on and put words on screen: click `micSelector`, then push one
 * still-forming turn down the intercepted socket.
 *
 * Deliberately a PARTIAL (`end_of_turn: false`), not a formatted final. A final
 * arms the auto-send countdown, which fires on a wall clock and would take the
 * transcript — and the very controls a spec is measuring — off screen mid-run. A
 * partial holds the speaking arrangement open indefinitely.
 */
export async function speak(page: Page, micSelector: string, text: string): Promise<void> {
  await page.click(micSelector);
  let ws = speechSockets.get(page);
  for (let attempt = 0; attempt < 100 && ws === undefined; attempt += 1) {
    await page.waitForTimeout(50);
    ws = speechSockets.get(page);
  }
  expect(ws, 'the STT socket never opened — is the fake mic device wired up?').not.toBeUndefined();
  ws?.send(
    JSON.stringify({
      type: 'Turn',
      transcript: text,
      end_of_turn: false,
      turn_is_formatted: false,
    }),
  );
  // The send button is the arrangement's tell: it renders on the first word,
  // whichever surface the mic was tapped on.
  await page.waitForSelector("[data-role='dock-send']");
  await settle(page);
}

/** Let the layout settle: two animation frames, so a ResizeObserver that
 * publishes a dynamic height (`--dock-overlay-height`) has been through a full
 * measure → write → re-layout cycle before anything is read. */
export async function settle(page: Page): Promise<void> {
  await page.evaluate(
    async () =>
      new Promise<void>((resolve) => {
        requestAnimationFrame(() => {
          requestAnimationFrame(() => {
            resolve();
          });
        });
      }),
  );
}

/**
 * Wait until an element's box stops moving, then return it.
 *
 * The ticket sheet slides in under a vaul-injected transform, so a box read on
 * the frame it opens is a box mid-animation — off the right edge of the window,
 * or a viewport-height below the bottom of it. Every assertion here is about
 * where a thing comes to REST, so the specs wait for rest rather than sleeping
 * for a duration that will be wrong on a slower machine.
 */
export async function stableBox(page: Page, selector: string): Promise<Box> {
  let previous: Box | null = null;
  for (let attempt = 0; attempt < 60; attempt += 1) {
    const current = await maybeBox(page, selector);
    if (
      current !== null &&
      previous !== null &&
      current.top === previous.top &&
      current.left === previous.left &&
      current.width === previous.width &&
      current.height === previous.height
    ) {
      return current;
    }
    previous = current;
    await settle(page);
  }
  expect(previous, `${selector} never settled`).not.toBeNull();
  return previous ?? { top: 0, right: 0, bottom: 0, left: 0, width: 0, height: 0 };
}

/** The element's viewport box. Fails the spec if it isn't on screen — a missing
 * anchor is a layout failure, not a reason to skip an assertion. */
export async function box(page: Page, selector: string): Promise<Box> {
  const found = await maybeBox(page, selector);
  expect(found, `no box for ${selector} — is it rendered?`).not.toBeNull();
  return found ?? { top: 0, right: 0, bottom: 0, left: 0, width: 0, height: 0 };
}

/** The element's viewport box, or null when it isn't on screen. */
export async function maybeBox(page: Page, selector: string): Promise<Box | null> {
  return page.evaluate((sel) => {
    const el = document.querySelector(sel);
    if (el === null) {
      return null;
    }
    const r = el.getBoundingClientRect();
    return {
      top: r.top,
      right: r.right,
      bottom: r.bottom,
      left: r.left,
      width: r.width,
      height: r.height,
    };
  }, selector);
}

/**
 * What actually PAINTS at a viewport point: the `data-role` of the topmost
 * element the browser's own hit test finds there (or its tag name, unnamed).
 *
 * This is the half of layering that boxes cannot answer. Two surfaces can
 * overlap by design — the toast band is *supposed* to stand in the feed's bottom
 * reserve — and the only question that matters is which of them the user sees.
 */
export async function paintedAt(page: Page, x: number, y: number): Promise<string | null> {
  return page.evaluate(
    ({ px, py }) => {
      const el = document.elementFromPoint(px, py);
      if (el === null) {
        return null;
      }
      const named = el.closest('[data-role]');
      return named === null ? el.tagName : named.getAttribute('data-role');
    },
    { px: x, py: y },
  );
}

/** Do two boxes share any area at all? */
export function intersects(a: Box, b: Box): boolean {
  return a.left < b.right && b.left < a.right && a.top < b.bottom && b.top < a.bottom;
}

/** A published layer rung, read off the document root — the single scale in
 * tokens.css that the stylesheets consume instead of writing literals. */
export async function layer(page: Page, name: string): Promise<number> {
  const value = await page.evaluate(
    (token) => getComputedStyle(document.documentElement).getPropertyValue(token).trim(),
    `--${name}`,
  );
  expect(value, `--${name} is not published in tokens.css`).not.toBe('');
  return Number(value);
}

/**
 * A resolved computed style property — the value the cascade actually landed on,
 * not the token it was spelled with.
 *
 * This is what lets two stylesheets be held to one claim. The rules that paint a
 * ticket's mark in a list (PrimaryScreen.css) and the badge in its own detail
 * sheet (TicketDetail.css) are different rules in different files that are
 * supposed to agree; comparing the token NAMES they mention only proves they
 * quote the same variable, while comparing what the browser resolved proves they
 * paint the same colour.
 */
export async function computed(page: Page, selector: string, property: string): Promise<string> {
  const value = await page.evaluate(
    ({ sel, prop }) => {
      const el = document.querySelector(sel);
      return el === null ? null : getComputedStyle(el).getPropertyValue(prop).trim();
    },
    { sel: selector, prop: property },
  );
  expect(value, `no element for ${selector}`).not.toBeNull();
  return value ?? '';
}

/** The z-index the browser actually computed for an element. */
export async function computedLayer(page: Page, selector: string): Promise<number> {
  const value = await page.evaluate((sel) => {
    const el = document.querySelector(sel);
    return el === null ? null : getComputedStyle(el).zIndex;
  }, selector);
  expect(value, `no element for ${selector}`).not.toBeNull();
  return Number(value);
}
