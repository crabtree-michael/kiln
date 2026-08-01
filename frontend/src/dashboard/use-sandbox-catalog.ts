// Per-project sandbox catalog (sandbox selection, 12 §3.2): one project card's
// snapshot picker + dev-box capture state. Kept per-project — not in the global
// dashboard store — because each project resolves its own coding-agent provider,
// so its snapshot catalog and dev boxes are distinct (a user with an Amika
// project and a Devin project sees a picker on the former and none on the latter).
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
   * snapshot handle. */
  catalogAvailable: boolean;
  /** This project's running dev boxes, loaded lazily by `refreshDevBoxes`. */
  devBoxes: DevBox[];
  /** (Re)loads `devBoxes` for the capture form. */
  refreshDevBoxes: () => Promise<void>;
  /** Captures a dev box as a new snapshot, then reloads `snapshots` so the new
   * (capturing) snapshot appears in the picker. */
  saveSnapshot: (body: SaveSnapshotRequest) => Promise<void>;
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
      // Best-effort: leave the dev-box list as-is.
    }
  }, [projectId]);

  const saveSnapshot = useCallback(
    async (body: SaveSnapshotRequest): Promise<void> => {
      await postSaveSnapshot(projectId, body);
      // Reload so the new (capturing) snapshot appears in the picker.
      await loadSnapshots();
    },
    [projectId, loadSnapshots],
  );

  return { snapshots, catalogAvailable, devBoxes, refreshDevBoxes, saveSnapshot };
}
