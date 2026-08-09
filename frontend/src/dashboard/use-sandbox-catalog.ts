// Per-project sandbox catalog (sandbox selection, 12 §3.2): one project card's
// snapshot picker. Kept per-project — not in the global dashboard store —
// because each project resolves its own coding-agent provider, so its snapshot
// catalog is distinct (a user with an Amika project and a Devin project sees a
// picker on the former and none on the latter).
//
// Capturing a running sandbox as a snapshot used to live here too, as a form on
// the project card. It is gone: saving a sandbox is a per-TICKET choice now (the
// ticket detail sheet's sandbox switch), not a project-level setting — and the
// capture it triggers runs on the server when the ticket is done, so this list
// can gain a `<project>-<timestamp>` entry (and the project's selection can move
// onto it) with nothing typed on this card.
import { useCallback, useEffect, useState } from 'react';
import { fetchSnapshots } from '@/transport/transport';
import type { Snapshot } from '@/transport/transport';

export interface SandboxCatalog {
  /** The base-image snapshots this project's workers can start from. Empty until
   * loaded, or when the provider has no catalog. */
  snapshots: Snapshot[];
  /** `true` once this project's provider is known to expose a snapshot catalog
   * (the snapshots endpoint answered 200). `false` while loading or when the
   * provider offers none (404) — the project form then falls back to a free-text
   * snapshot handle. */
  catalogAvailable: boolean;
}

/** Loads one project's snapshot catalog. `fetchSnapshots` resolves `null` when
 * the project's provider offers no catalog (404) — the form then falls back to a
 * free-text handle — so a null keeps `catalogAvailable` false. A read failure
 * leaves the state as-is (the form still works via the text fallback) rather than
 * surfacing a scary error. */
export function useSandboxCatalog(projectId: string): SandboxCatalog {
  const [snapshots, setSnapshots] = useState<Snapshot[]>([]);
  const [catalogAvailable, setCatalogAvailable] = useState(false);

  const loadSnapshots = useCallback(
    async (isCancelled?: () => boolean): Promise<void> => {
      try {
        const list = await fetchSnapshots(projectId);
        if (isCancelled?.() === true) {
          return;
        }
        setCatalogAvailable(list !== null);
        setSnapshots(list ?? []);
      } catch {
        // Best-effort: leave the catalog unavailable and the picker hidden.
      }
    },
    [projectId],
  );

  // Load the catalog on mount and whenever the project changes.
  useEffect(() => {
    let cancelled = false;
    void loadSnapshots(() => cancelled);
    return () => {
      cancelled = true;
    };
  }, [loadSnapshots]);

  return { snapshots, catalogAvailable };
}
