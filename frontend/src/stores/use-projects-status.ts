// Cross-project ambient status for the desktop projects rail (13 §5, §8).
//
// The rail's second job is to answer "is anything wrong anywhere" without the
// user opening anything — which means it needs state for projects the app is
// deliberately *not* scoped to. Everything else in the client is single-project
// by construction (one EventSource, one board store, one feed store, all keyed
// on the current `project_id`, 12 §4.1), so this hook is the one place that
// reaches sideways.
//
// **How it gets the data, and why it is a poll.** The server has no
// cross-project status endpoint today, and 13 §11 is explicit that desktop is
// "frontend only, in the main" — landing on contracts that already exist. So
// this fetches each *other* project's board snapshot on a slow interval and
// derives its state locally. The currently-selected project is never polled: it
// already has a live stream, so its state comes from the board store for free
// and updates instantly.
//
// The cost is bounded by design: one small GET per non-selected project per
// `POLL_INTERVAL_MS`, nothing at all for a single-project user (the overwhelming
// case), and nothing while the tab is hidden. If projects-per-user ever grows
// past a handful, the right fix is one server-side status endpoint, not a
// tighter interval here.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { fetchProjectBoard, type Board, type MeProject } from '@/transport/transport';
import { deriveProjectState, type ProjectState } from '@/stores/project-status';

/**
 * How often the non-selected projects are re-read. Deliberately slow: the rail
 * is peripheral-vision furniture, not a monitor. A minute-old "quiet" is still a
 * true statement about a project nobody is looking at, and the project that
 * actually needs the user has a push notification (02 §10) doing the urgent
 * half of this job already.
 */
export const POLL_INTERVAL_MS = 60_000;

/**
 * Per-project rail state, keyed by `project_id`. Projects not yet heard from map
 * to `unknown`, which the rail renders as absence — see `ProjectState`.
 */
export type ProjectStates = Record<string, ProjectState>;

/**
 * The last reading seen for each project, surviving this hook's remounts.
 *
 * Module scope is doing one specific job here and is not a back door for state:
 * the client still holds no authoritative anything (02 §11), and every value in
 * here is a *derived cache* of a board snapshot the server owns. What it buys is
 * that switching projects — which deliberately remounts the whole data subtree —
 * doesn't blank the rail's other rows while their boards are re-fetched. It is
 * per-JS-context, so two tabs stay independent (12 §7.2), and it is only ever
 * read as a seed: the poll below overwrites it within a round-trip.
 */
const lastKnownStates = new Map<string, ProjectState>();

/** Clears the cross-remount seed. Test-only: the cache is per-JS-context and a
 * vitest module registry is shared across cases in a file, so without this one
 * case's readings would leak into the next one's first paint. */
export function resetProjectStatesCache(): void {
  lastKnownStates.clear();
}

/**
 * Resolves every project's rail state (13 §5).
 *
 * @param projects the user's projects, from the session/current-project store.
 * @param currentId the selected project — excluded from polling, since its live
 *   board is already on screen.
 * @param currentBoard the selected project's live board snapshot (`null` before
 *   the first one lands).
 */
export function useProjectsStatus(
  projects: MeProject[],
  currentId: string | null,
  currentBoard: Board | null,
): ProjectStates {
  // Only the polled (non-selected) projects live in state; the selected one is
  // folded in at the end from the live board, so a stream update repaints its
  // row without waiting on — or churning — this map.
  //
  // Seeded from the module cache below rather than from `{}`, because this hook
  // is remounted on every project switch: `CurrentProjectProvider` keys its
  // subtree by the current id precisely so the stream and the stores tear down
  // and re-open (12 §4.1), and the desktop shell rides inside that subtree. A
  // bare `{}` would therefore blank every OTHER project's mark for the length of
  // one round-trip each time the user switches — which is the exact moment they
  // are looking at the rail. The last reading is still true until a new one says
  // otherwise, so it survives the remount.
  const [polled, setPolled] = useState<ProjectStates>(() => Object.fromEntries(lastKnownStates));

  // The ids to poll, as a stable primitive: an array identity would re-arm the
  // effect on every render of the parent, restarting the interval each time.
  const pollIdsKey = useMemo(
    () =>
      projects
        .map((project) => project.id)
        .filter((id) => id !== currentId)
        .join(','),
    [projects, currentId],
  );

  // Guards a stale write from an in-flight fetch whose project has since been
  // deselected (or deleted). Bumped on every effect re-arm; a response carrying
  // an older generation is dropped.
  const generationRef = useRef(0);

  const readAll = useCallback((ids: string[], generation: number): void => {
    ids.forEach((id) => {
      void fetchProjectBoard(id)
        .then((board) => {
          if (generationRef.current !== generation) {
            return;
          }
          const state = deriveProjectState(board);
          lastKnownStates.set(id, state);
          setPolled((previous) => ({ ...previous, [id]: state }));
        })
        .catch(() => {
          // A failed read leaves the previous reading in place rather than
          // demoting the row to `unknown`: a transient blip is not news, and
          // flickering a project's mark off and back on is exactly the twitchy
          // behaviour an ambient surface must not have. A project that has never
          // been read simply stays absent from the map (→ `unknown`).
        });
    });
  }, []);

  useEffect(() => {
    const ids = pollIdsKey === '' ? [] : pollIdsKey.split(',');
    const generation = generationRef.current + 1;
    generationRef.current = generation;
    if (ids.length === 0) {
      return;
    }

    readAll(ids, generation);
    const timer = window.setInterval(() => {
      // Don't poll a tab nobody is looking at — the whole point of this hook is
      // peripheral vision, and a backgrounded tab has none. The
      // visibility listener below catches it up the moment it returns.
      if (document.visibilityState === 'hidden') {
        return;
      }
      readAll(ids, generation);
    }, POLL_INTERVAL_MS);

    // Coming back to the tab is exactly when the rail is glanced at, so refresh
    // then rather than making the user wait out the rest of an interval.
    const onVisible = (): void => {
      if (document.visibilityState === 'visible') {
        readAll(ids, generation);
      }
    };
    document.addEventListener('visibilitychange', onVisible);

    return () => {
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [pollIdsKey, readAll]);

  return useMemo(() => {
    const states: ProjectStates = {};
    projects.forEach((project) => {
      states[project.id] =
        project.id === currentId
          ? deriveProjectState(currentBoard)
          : (polled[project.id] ?? 'unknown');
    });
    return states;
  }, [projects, currentId, currentBoard, polled]);
}
