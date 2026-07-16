// Current-project store (12 §4.1, DP5): the client owns which of the user's
// projects the app is viewing, referenced by `project_id`. It reads the live set
// from the session store, resolves the current one (a deep-link `?project=` from
// a notification tap (12 §6.3), else the localStorage MRU, else the first by
// created_at), and scopes every project-scoped transport call to it via
// `setActiveProjectId`. Switching is instant and client-side; the subtree is
// keyed by the current id so the data providers tear down and re-open the single
// EventSource against the new project's stream and refetch board/feed (12 §4.1).
import { Fragment, useCallback, useMemo, useState, type JSX, type ReactNode } from 'react';
import { setActiveProjectId } from '@/transport/transport';
import type { MeProject } from '@/transport/transport';
import { useSession } from '@/stores/session-context';
import {
  CurrentProjectContext,
  type CurrentProjectStoreValue,
} from '@/stores/current-project-context';

/** localStorage key for the most-recently-used project id (12 §4.1). */
const STORAGE_KEY = 'kiln.currentProjectId';

/** The preferred project id at load: a `?project=` deep-link (a notification
 * tap, 12 §6.3) wins over the persisted MRU. Null when neither is present or
 * storage is unavailable. */
function readPreferredId(): string | null {
  try {
    const param = new URLSearchParams(window.location.search).get('project');
    if (param !== null && param !== '') {
      return param;
    }
  } catch {
    // Non-browser env (SSR/test) — fall through to storage.
  }
  try {
    return window.localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

/** Persists the MRU project id, best-effort (storage may be unavailable). */
function persistId(id: string): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, id);
  } catch {
    // Best-effort: a private-mode storage failure just loses the MRU default.
  }
}

export interface CurrentProjectProviderProps {
  children: ReactNode;
}

export function CurrentProjectProvider({ children }: CurrentProjectProviderProps): JSX.Element {
  const { me } = useSession();
  const projects = useMemo<MeProject[]>(() => me?.projects ?? [], [me]);
  const [selectedId, setSelectedId] = useState<string | null>(() => readPreferredId());

  // Resolve the current project: the selected id when it still names a live
  // project (it may have been deleted since selection), else the first by
  // created_at (12 §4.1).
  const current = useMemo<MeProject | null>(() => {
    const byId = projects.find((p) => p.id === selectedId);
    return byId ?? projects[0] ?? null;
  }, [projects, selectedId]);

  // Scope every project-scoped transport call to the current project. Set during
  // render — NOT in an effect — so it is already in place before the child data
  // providers' mount effects open the stream / fetch the board (child effects run
  // before parent effects; render runs before both). Idempotent.
  setActiveProjectId(current?.id ?? null);

  const selectProject = useCallback((id: string): void => {
    setSelectedId(id);
    persistId(id);
  }, []);

  const value = useMemo<CurrentProjectStoreValue>(
    () => ({ current, projects, selectProject }),
    [current, projects, selectProject],
  );

  // Key the subtree by the current id so a switch remounts the data providers,
  // re-opening the EventSource against the new project and refetching (12 §4.1).
  return (
    <CurrentProjectContext.Provider value={value}>
      <Fragment key={current?.id ?? 'none'}>{children}</Fragment>
    </CurrentProjectContext.Provider>
  );
}
