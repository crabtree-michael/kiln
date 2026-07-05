// A thin Amika v0beta1 client for the e2e suite.
//
// Amika is normally sealed behind the agent-runtime abstraction (05 §1) — no module
// outside internal/agent may know it exists. The tests deliberately reach past that
// seam for exactly two reasons, both scoped to /tests:
//   1. ready-kicks-off-amika-run asserts the real send landed (Amika is the default
//      provider, and we want to verify that path end-to-end).
//   2. global-teardown destroys the kiln-worker-* sandbox pool afterwards (auto_delete
//      is off by design — 05 D6 — so nothing self-cleans).
//
// v0beta1 has NO list-jobs endpoint, so an agent-send-job (the send) isn't directly
// observable. Its side effect is: a fresh pull's first turn is new_session=true, which
// opens a session on the bound sandbox. GET /sandboxes/{id}/sessions ({sessions,total})
// is therefore the external proof a message was sent. See backend/internal/agent/amika.
const DEFAULT_BASE_URL = 'https://app.amika.dev/api/v0beta1';
// The stack-under-test's worker-name scope (05 §4, amended 2026-07-05). Must match
// the backend's KILN_WORKER_PREFIX — the docker-compose default is kiln-dev-worker- —
// so the teardown only ever deletes THIS stack's pool, never another environment's
// (e.g. prod's) sandboxes on the shared Amika account.
export const WORKER_NAME_PREFIX = process.env.KILN_WORKER_PREFIX ?? 'kiln-dev-worker-';

export type AmikaConfig = { base: string; apiKey: string };

// Reads AMIKA_API_KEY / AMIKA_BASE_URL from the env (loaded from the repo-root .env by
// playwright.config.ts). Returns null when no key is set — callers decide whether that
// is a skip (teardown) or a failure (the real-Amika test).
export function amikaConfig(): AmikaConfig | null {
  const apiKey = process.env.AMIKA_API_KEY;
  if (!apiKey) return null;
  const base = (process.env.AMIKA_BASE_URL ?? DEFAULT_BASE_URL).replace(/\/+$/, '');
  return { base, apiKey };
}

function authHeaders(cfg: AmikaConfig): Record<string, string> {
  return { Authorization: `Bearer ${cfg.apiKey}`, Accept: 'application/json' };
}

export type Sandbox = { id: string; name: string; current_session_id: string | null };

// Every sandbox this stack owns (name-prefixed kiln-worker-*), the fixed worker pool.
export async function listKilnSandboxes(cfg: AmikaConfig): Promise<Sandbox[]> {
  const res = await fetch(`${cfg.base}/sandboxes`, { headers: authHeaders(cfg) });
  if (!res.ok) throw new Error(`amika list sandboxes: ${res.status} ${await res.text()}`);
  const all = (await res.json()) as Sandbox[];
  return all.filter((s) => s.name?.startsWith(WORKER_NAME_PREFIX));
}

// The number of sessions on a sandbox. A successful new_session send makes this grow by
// one — the observable that agent-runtime's StartTurn (POST …/agent-send-jobs) reached
// Amika. v0beta1 has no list-jobs endpoint, so this is the send's external footprint.
export async function sessionCount(cfg: AmikaConfig, sandboxId: string): Promise<number> {
  const res = await fetch(`${cfg.base}/sandboxes/${encodeURIComponent(sandboxId)}/sessions`, {
    headers: authHeaders(cfg),
  });
  if (!res.ok) throw new Error(`amika sessions ${sandboxId}: ${res.status} ${await res.text()}`);
  // v0beta1 returns `total` as a STRING (e.g. "0"), so coerce; fall back to the array.
  const body = (await res.json()) as { total?: number | string; sessions?: unknown[] };
  const total = body.total != null ? Number(body.total) : NaN;
  return Number.isFinite(total) ? total : (body.sessions?.length ?? 0);
}

// Deletes a sandbox by id or name. A 404 means it is already gone (success). Returns
// what happened so callers can log it.
export async function deleteSandbox(cfg: AmikaConfig, idOrName: string): Promise<'deleted' | 'gone'> {
  const res = await fetch(`${cfg.base}/sandboxes/${encodeURIComponent(idOrName)}`, {
    method: 'DELETE',
    headers: authHeaders(cfg),
  });
  if (res.ok) return 'deleted';
  if (res.status === 404) return 'gone';
  throw new Error(`amika delete ${idOrName}: ${res.status} ${await res.text()}`);
}
