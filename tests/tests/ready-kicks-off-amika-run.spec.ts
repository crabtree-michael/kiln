import { expect, test, type APIRequestContext } from '@playwright/test';
import { amikaConfig, listKilnSandboxes, sessionCount, type AmikaConfig } from '../amika';

// E2E: moving a ticket to Ready kicks off a REAL Amika run (docs/specs/01 §2 → 05).
//
// There is no UI or HTTP endpoint to move a ticket — the board is read-only and all
// mutation flows through the brain (D5). So this drives the real brain over HTTP:
// POST /api/message with a fully-specified request → the brain calls create_ticket +
// mark_ready in one turn → mark_ready emits pull.evaluate → the deterministic pull
// binds a free worker, moves the ticket ready→working, and emits agent.send → the
// agent-runtime's StartTurn does POST /sandboxes/{id}/agent-send-jobs on Amika.
//
// Two signals are asserted:
//   1. Board: the tagged ticket reaches `working` (the pull bound a worker and emitted
//      agent.send). Developing = working|blocked; working is the pull's landing state.
//   2. Amika (the real send): the bound sandbox gains a new session. Amika is the
//      default provider and we want to verify that path actually works; v0beta1 has no
//      list-jobs endpoint, so the new_session opened by the pull's first turn — visible
//      via GET /sandboxes/{id}/sessions — is the external proof the message was sent.
//      This deliberately leaks a provider detail (05 §1), justified because the point of
//      the test is to confirm the real Amika default.
//
// This reaches Developing, so it exercises real Amika and bills money. Run against the
// real stack on the cheap model (KILN_BRAIN_MODEL=claude-haiku-4-5-...); the global
// teardown destroys the kiln-worker-* sandboxes afterwards. See ./README.md.

// API-driven: hit the backend directly (the vite proxy at :5173 is for the browser
// client, which has no affordance to move a ticket). Override with KILN_E2E_API_URL.
const apiBase = (process.env.KILN_E2E_API_URL ?? 'http://localhost:8080').replace(/\/+$/, '');

// The slice of GET /api/board (wire.Board) this test reads.
type Ticket = { title: string; state: string };
type Board = { working: Ticket[]; ready: Ticket[]; worker_free: number; worker_total: number };

async function getBoard(request: APIRequestContext): Promise<Board> {
  const res = await request.get(`${apiBase}/api/board`);
  expect(res.ok(), `GET /api/board -> ${res.status()}`).toBeTruthy();
  return (await res.json()) as Board;
}

// Session count per pooled sandbox, tolerant of a sandbox that momentarily can't be read
// (e.g. waking from auto-stop): a transient error is treated as "no change yet" so it
// doesn't fail the poll. Hard errors surfaced at snapshot time (below) still fail fast.
async function sessionCountsBySandbox(cfg: AmikaConfig): Promise<Map<string, number>> {
  const counts = new Map<string, number>();
  for (const s of await listKilnSandboxes(cfg)) {
    try {
      counts.set(s.id, await sessionCount(cfg, s.id));
    } catch {
      // leave unset; poll comparison treats a missing prior as 0
    }
  }
  return counts;
}

test('moving a ticket to Ready kicks off a real Amika run', async ({ request }) => {
  test.setTimeout(180_000); // two sequential real-service poll windows (board, then Amika)

  // This test verifies the REAL Amika send path (the default provider), so it needs
  // Amika creds — a missing key is a misconfigured run, not a pass.
  const amika = amikaConfig();
  expect(
    amika,
    'AMIKA_API_KEY is unset — this test verifies the real Amika send; set it (repo-root .env)',
  ).not.toBeNull();
  const cfg = amika as AmikaConfig;

  // Precondition: the pull can only bind if a worker is free. On a persistent stack a
  // prior run may have left the whole pool busy — surface that as a clear stack signal,
  // not a mystery timeout on the assertions below. Don't loosen this; reset the stack.
  const initial = await getBoard(request);
  expect(
    initial.worker_free,
    `no free worker (${initial.worker_free}/${initial.worker_total}) — a prior run left the ` +
      `pool busy; reset the stack before running`,
  ).toBeGreaterThan(0);

  // Snapshot each pooled sandbox's session count BEFORE the send, so we can prove a NEW
  // session (a new_session agent-send-job) appears as a direct result. A hard error here
  // (bad key/URL, unreachable Amika) fails fast.
  const sandboxes = await listKilnSandboxes(cfg);
  expect(
    sandboxes.length,
    'no kiln-worker-* sandboxes exist — is the stack running on the real Amika provider?',
  ).toBeGreaterThan(0);
  const before = new Map<string, number>();
  for (const s of sandboxes) before.set(s.id, await sessionCount(cfg, s.id));

  // Tag THIS request so we assert on our own ticket regardless of what is already on the
  // persistent board (an identical request is, correctly, de-duplicated by the brain).
  const tag = `E2E-${Date.now().toString(36).toUpperCase()}`;

  // Act: tell the brain the work is fully specified and to start it immediately, so it
  // creates the ticket AND marks it ready in the one turn.
  const post = await request.post(`${apiBase}/api/message`, {
    data: {
      text:
        `Create a ticket to add a GET /health endpoint that returns 200 with the body "ok", ` +
        `and start work on it right away — it is fully specified, so mark it ready immediately. ` +
        `Put the exact tag ${tag} in the ticket title.`,
    },
  });
  expect(post.status(), `POST /api/message -> ${post.status()}`).toBe(202);

  // Signal 1 (board): our tagged ticket reaches `working` — the pull bound a worker and
  // emitted agent.send. A real-LLM turn plus the pull is slow; poll within the config's
  // generous expect window.
  await expect
    .poll(async () => (await getBoard(request)).working.some((t) => t.title.includes(tag)), {
      message: `ticket tagged ${tag} never reached the Working (Developing) column`,
    })
    .toBe(true);

  // Signal 2 (Amika): agent-runtime's StartTurn ran — some pooled sandbox gained a new
  // session vs its pre-send count, proving the agent-send-job (the message) reached
  // Amika. StartTurn happens on the agent module's poller shortly AFTER `working`, so
  // give it its own window. A sandbox recreated on release would carry a new id absent
  // from `before` (prior treated as 0), so re-listing here stays correct.
  await expect
    .poll(
      async () => {
        for (const [id, count] of await sessionCountsBySandbox(cfg)) {
          if (count > (before.get(id) ?? 0)) return true;
        }
        return false;
      },
      {
        message:
          'no kiln-worker-* sandbox gained a new Amika session — the agent-send-job ' +
          '(message) was never sent to Amika',
        timeout: 90_000,
      },
    )
    .toBe(true);
});
