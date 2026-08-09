// Per-project sandbox catalog (sandbox selection, 12 §3.2): one project card's
// snapshot picker, its running dev boxes, and the capture that turns one into
// the other. Kept per-project — not in the global dashboard store — because each
// project resolves its own coding-agent provider, so its snapshot catalog and
// dev boxes are distinct (a user with an Amika project and a Devin project sees
// a picker on the former and none on the latter).
//
// There are TWO ways a snapshot gets into this list, and they are different
// affordances rather than two spellings of one:
//
//   * this capture — a running dev box the user names now, on the project's own
//     settings. It is what a workspace worth keeping is saved with, at the
//     moment they decide it is worth keeping;
//   * the per-ticket sandbox option, whose capture runs on the SERVER when the
//     ticket leaves Developing, so the list can gain an entry (and the project's
//     selection can move onto it) with nothing typed on this card.
//
// The capture is destructive at the provider — it scrubs the dev box's injected
// secrets and deletes it — so the form that calls `saveSnapshot` gates it behind
// a confirm naming that. This hook does not: it is the seam, not the decision.
import { useCallback, useEffect, useState } from 'react';
import {
  fetchDevBoxes,
  fetchSnapshots,
  saveSnapshot as postSaveSnapshot,
} from '@/transport/transport';
import type { DevBox, SaveSnapshotRequest, Snapshot } from '@/transport/transport';

export interface SandboxCatalog {
  /** The base-image snapshots this project's workers can start from. Empty until
   * loaded, or when the provider has no catalog. */
  snapshots: Snapshot[];
  /** `true` once this project's provider is known to expose a snapshot catalog
   * (the snapshots endpoint answered 200). `false` while loading or when the
   * provider offers none (404) — the project form then falls back to a free-text
   * snapshot handle, and offers no capture. */
  catalogAvailable: boolean;
  /** This project's running dev boxes — the sandboxes a snapshot can be captured
   * from, loaded by `refreshDevBoxes`. Never a pooled Kiln worker: the server
   * filters those out. */
  devBoxes: DevBox[];
  /** (Re)loads `devBoxes` for the capture form. */
  refreshDevBoxes: () => Promise<void>;
  /** Captures a dev box as a new snapshot, then reloads both lists: the new
   * (capturing) snapshot appears in the picker, and the consumed dev box leaves
   * the capture form's select. Rejects when the capture never started, so the
   * form can say so rather than clearing itself on a failure. */
  saveSnapshot: (body: SaveSnapshotRequest) => Promise<Snapshot>;
}

/** Loads and manages one project's snapshot catalog. `fetchSnapshots` resolves
 * `null` when the project's provider offers no catalog (404) — the form then
 * falls back to a free-text handle — so a null keeps `catalogAvailable` false. A
 * read failure leaves the state as-is (the form still works via the text
 * fallback) rather than surfacing a scary error. */
export function useSandboxCatalog(projectId: string): SandboxCatalog {
  const [snapshots, setSnapshots] = useState<Snapshot[]>([]);
  const [catalogAvailable, setCatalogAvailable] = useState(false);
  const [devBoxes, setDevBoxes] = useState<DevBox[]>([]);

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

  const refreshDevBoxes = useCallback(async (): Promise<void> => {
    try {
      setDevBoxes((await fetchDevBoxes(projectId)) ?? []);
    } catch {
      // Best-effort: leave the dev-box list as-is. The capture form reads an
      // empty list as "nothing to capture", which is the honest thing to show
      // when we could not ask.
    }
  }, [projectId]);

  const saveSnapshot = useCallback(
    async (body: SaveSnapshotRequest): Promise<Snapshot> => {
      const snapshot = await postSaveSnapshot(projectId, body);
      // Reload both: the new (capturing) snapshot belongs in the picker, and the
      // dev box it was captured from no longer exists to be captured again.
      await Promise.all([loadSnapshots(), refreshDevBoxes()]);
      return snapshot;
    },
    [projectId, loadSnapshots, refreshDevBoxes],
  );

  return { snapshots, catalogAvailable, devBoxes, refreshDevBoxes, saveSnapshot };
}
