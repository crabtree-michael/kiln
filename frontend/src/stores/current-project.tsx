// Current-project store (12 §4.1, DP5): the client owns which of the user's
// projects the app is viewing, referenced by `project_id`. It reads the live set
// from the session store, resolves the current one (a deep-link `?project=` from
// a notification tap (12 §6.3), else the localStorage MRU, else the first by
// created_at), and scopes every project-scoped transport call to it via
// `setActiveProjectId`. Switching is instant and client-side; the subtree is
// keyed by the current id so the data providers tear down and re-open the single
// EventSource against the new project's stream and refetch board/feed (12 §4.1).
//
// It also owns the project half of a notification tap (12 §6.3): a tap must land
// the app on the project whose event fired it, never leave the user reading a
// different project's board. Both arrival paths are handled — the cold open via
// `?project=` below, and a live tap in an already-open tab via the service
// worker's `kiln:navigate` message.
import {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useState,
  type JSX,
  type ReactNode,
} from 'react';
import { setActiveProjectId } from '@/transport/transport';
import type { MeProject } from '@/transport/transport';
import { useSession } from '@/stores/session-context';
import { parseDeepLink, stashDeepLinkTicket, subscribeDeepLink } from '@/stores/deep-link';
import {
  CurrentProjectContext,
  type CurrentProjectStoreValue,
} from '@/stores/current-project-context';

/** localStorage key for the most-recently-used project id (12 §4.1). */
const STORAGE_KEY = 'kiln.currentProjectId';

/** The project id a `?project=` deep-link names (a notification tap opened cold,
 * 12 §6.3), or null on a plain visit. */
function deepLinkedId(): string | null {
  try {
    return parseDeepLink(window.location.search).projectId;
  } catch {
    // Non-browser env (SSR/test) — treat as a plain visit.
    return null;
  }
}

/** The persisted most-recently-used project id, or null when unset/unavailable. */
function readStoredId(): string | null {
  try {
    return window.localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

/** The preferred project id at load: a `?project=` deep-link (a notification
 * tap, 12 §6.3) wins over the persisted MRU. A deep-linked id is also written
 * through to the MRU, so the tap's switch is the same durable switch the header
 * switcher makes — a reload after the tap stays on the project it opened rather
 * than snapping back to the one the user was on before. */
function readPreferredId(): string | null {
  const deepLinked = deepLinkedId();
  if (deepLinked !== null) {
    persistId(deepLinked);
    return deepLinked;
  }
  return readStoredId();
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

  // A notification tapped while this tab is already open (12 §6.3): the service
  // worker hands us the deep link rather than reloading — which would drop the
  // live session (02 §10) — so the switch has to happen client-side, here. The
  // cold-open path needs none of this: `?project=` is already resolved above.
  const currentId = current?.id ?? null;
  useEffect(() => {
    return subscribeDeepLink((link) => {
      if (link.projectId === null || link.projectId === currentId) {
        // Ticketless, an older payload with no project, or already the project
        // on screen — nothing to switch; the screen opens the ticket in place.
        return;
      }
      if (!projects.some((p) => p.id === link.projectId)) {
        // A project this user doesn't own (or one deleted since the notification
        // fired). Selecting an unresolvable id would silently fall back to the
        // *first* project — a worse wrong answer than staying put, so stay put.
        return;
      }
      // Switching remounts the keyed subtree below, which discards the screen's
      // ticket state — park the tap's ticket so the remounted screen reopens it
      // against the right board.
      if (link.ticketId !== null) {
        stashDeepLinkTicket(link.ticketId);
      }
      selectProject(link.projectId);
    });
  }, [currentId, projects, selectProject]);

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
