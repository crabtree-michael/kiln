// The caller's connected GitHub account (settings repo picker): the repos their
// sign-in token can reach, which the project form lists instead of asking for a
// typed repo URL.
//
// User-scoped, NOT per-project (the contrast with `use-sandbox-catalog`, which
// must be per-project because each project resolves its own agent provider):
// there is one GitHub credential — the token from signing in — so one fetch
// serves every project card. Mount it once at the view level and pass the result
// down, rather than per `ProjectFields`.
import { useCallback, useEffect, useState } from 'react';
import { fetchGitHubRepos } from '@/transport/transport';
import type { GitHubRepo } from '@/transport/transport';

/** Where every "Connect GitHub" affordance sends the browser — the backend-owned
 * REPO-SCOPED OAuth grant, NOT `/auth/github/login`, which is the scopeless
 * sign-in and grants nothing (11 §2 D2). This is the only path that produces a
 * credential able to list, clone, and push repos, so the settings repo picker
 * and the Integrations card both point here; GitHub itself decides whether to
 * re-prompt for an account. It is a backend route, so any navigation to it must
 * be a real full-page load — never a router `Link`. */
export const GITHUB_CONNECT_PATH = '/auth/github/connect';

export interface GitHubRepos {
  /** The repos the connected account can reach, sorted by full name. Empty
   * while loading, when disconnected, or when the account genuinely has none. */
  repos: GitHubRepo[];
  /** `true` once GitHub has accepted the caller's credential and the list is
   * live. `false` covers both "still loading" and "must (re)authorize" — the
   * form pairs it with `loading` to tell a spinner from a connect prompt. */
  connected: boolean;
  /** `true` while the first (or a refreshed) fetch is in flight. */
  loading: boolean;
  /** Non-null when the fetch itself failed (a 502 from GitHub, a network blip).
   * Distinct from being disconnected: this is a real error worth showing, not a
   * prompt to reconnect. */
  error: string | null;
  /** Re-runs the fetch — used by the "Retry" affordance after a failure. */
  refresh: () => Promise<void>;
}

/** Loads the caller's GitHub repos once on mount. A disconnected account is a
 * normal 200 (`connected: false`), so it lands in state rather than in `error`;
 * only a failed request populates `error`. */
export function useGitHubRepos(): GitHubRepos {
  const [repos, setRepos] = useState<GitHubRepo[]>([]);
  const [connected, setConnected] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async (isCancelled?: () => boolean): Promise<void> => {
    setLoading(true);
    try {
      const list = await fetchGitHubRepos();
      if (isCancelled?.() === true) {
        return;
      }
      setConnected(list.connected);
      setRepos(list.repos);
      setError(null);
    } catch (err) {
      if (isCancelled?.() === true) {
        return;
      }
      // Leave `connected` alone: a failed refresh shouldn't flip a working
      // picker into the connect prompt and lose the user's current selection.
      setError(err instanceof Error ? err.message : 'could not load your GitHub repos');
    } finally {
      if (isCancelled?.() !== true) {
        setLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    void load(() => cancelled);
    return () => {
      cancelled = true;
    };
  }, [load]);

  const refresh = useCallback((): Promise<void> => load(), [load]);

  return { repos, connected, loading, error, refresh };
}
