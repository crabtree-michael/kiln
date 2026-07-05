// After the e2e suite: destroy every Amika sandbox the stack holds.
//
// Reaching Developing runs a real Amika turn, and auto_delete is OFF by design (05 D6,
// so a Blocked ticket's worker survives overnight) — nothing self-cleans, and leaked
// sandboxes keep billing. Sandbox names are stable slot uuids forming a fixed pool of
// KILN_WORKER_COUNT; a specific run's sandbox is indistinguishable from the pool by name
// (the join is slot-uuid → name, D5), so we delete them all.
//
// NOTE: while the backend is up, its ~60s reconciler recreates idle slots — so this is
// best-effort during a run; a fully clean slate is only guaranteed once the stack is
// down (`make down`). See ./README.md.
import type { FullConfig } from '@playwright/test';
import { amikaConfig, deleteSandbox, listKilnSandboxes, WORKER_NAME_PREFIX } from './amika';

async function globalTeardown(_config: FullConfig): Promise<void> {
  const cfg = amikaConfig();
  if (!cfg) {
    console.warn('[teardown] AMIKA_API_KEY unset — skipping Amika sandbox cleanup (mock stack?).');
    return;
  }

  const sandboxes = await listKilnSandboxes(cfg);
  if (sandboxes.length === 0) {
    console.log(`[teardown] no ${WORKER_NAME_PREFIX}* sandboxes to delete.`);
    return;
  }

  let deleted = 0;
  const failures: string[] = [];
  for (const s of sandboxes) {
    try {
      await deleteSandbox(cfg, s.id);
      deleted++;
    } catch (err) {
      failures.push(`${s.name} (${s.id}): ${(err as Error).message}`);
    }
  }
  console.log(`[teardown] deleted ${deleted}/${sandboxes.length} ${WORKER_NAME_PREFIX}* sandbox(es).`);
  if (failures.length > 0) {
    // A sandbox we failed to delete keeps billing — make the leak loud, don't swallow it.
    throw new Error(`[teardown] ${failures.length} sandbox(es) not deleted:\n  ${failures.join('\n  ')}`);
  }
}

export default globalTeardown;
