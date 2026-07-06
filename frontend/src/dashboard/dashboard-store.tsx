// Dashboard store (11 §5, §9): loads the signed-in `GET /api/me` account view
// on mount, and drives the settings/project save, connectivity verify, and
// sign-out actions — a straight transposition of the chat-store pattern
// (`useState` for the value pieces, `useCallback` actions, `useEffect`
// mount-load, `useMemo` context value).
import { useCallback, useEffect, useMemo, useState, type JSX, type ReactNode } from 'react';
import { fetchMe, postLogout, postVerify, putProject, putSettings } from '@/transport/transport';
import type {
  Me,
  ProjectUpdateRequest,
  SettingsUpdateRequest,
  VerifyCheck,
} from '@/transport/transport';
import {
  DashboardStoreContext,
  type DashboardPhase,
  type DashboardStoreValue,
} from '@/dashboard/dashboard-context';

export interface DashboardProviderProps {
  children: ReactNode;
}

export function DashboardProvider({ children }: DashboardProviderProps): JSX.Element {
  const [phase, setPhase] = useState<DashboardPhase>('loading');
  const [me, setMe] = useState<Me | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [verifying, setVerifying] = useState(false);
  const [verifyChecks, setVerifyChecks] = useState<VerifyCheck[] | null>(null);

  // Shared by the mount effect and `signOut`'s re-fetch: passes through
  // `loading` while `GET /api/me` is in flight (the documented phase
  // contract), then lands on `signed-out`/`ready`. Saves deliberately do NOT
  // go through here — they swap in the returned `Me` directly, so a save
  // never flashes a loading state. `isCancelled` lets the mount effect drop
  // the result after unmount (chat-store's `cancelled`-flag pattern).
  //
  // `fetchMe` only returns normally for the 200/401 cases (transport.ts
  // resolves 401 to `null`); anything else — a 500, a network blip, an
  // unconfigured deployment's 404 — rejects. Left uncaught, that rejection
  // would strand `phase` at `'loading'` forever (final review, Important
  // #2): catch it, surface a readable `error`, and land on `signed-out` so
  // the loading view never spins indefinitely.
  const load = useCallback(async (isCancelled?: () => boolean): Promise<void> => {
    setPhase('loading');
    try {
      const account = await fetchMe();
      if (isCancelled?.() === true) {
        return;
      }
      setMe(account);
      setPhase(account === null ? 'signed-out' : 'ready');
    } catch (err) {
      if (isCancelled?.() === true) {
        return;
      }
      setMe(null);
      setError(err instanceof Error ? err.message : 'load failed');
      setPhase('signed-out');
    }
  }, []);

  // Load the account view on mount (11 §5).
  useEffect(() => {
    let cancelled = false;
    void load(() => cancelled);
    return () => {
      cancelled = true;
    };
  }, [load]);

  const saveSettings = useCallback(async (body: SettingsUpdateRequest): Promise<void> => {
    setSaving(true);
    setError(null);
    try {
      const updated = await putSettings(body);
      setMe(updated);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'saveSettings failed');
    } finally {
      setSaving(false);
    }
  }, []);

  const saveProject = useCallback(async (body: ProjectUpdateRequest): Promise<void> => {
    setSaving(true);
    setError(null);
    try {
      const updated = await putProject(body);
      setMe(updated);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'saveProject failed');
    } finally {
      setSaving(false);
    }
  }, []);

  const runVerify = useCallback(async (): Promise<void> => {
    setVerifying(true);
    setError(null);
    try {
      const response = await postVerify();
      setVerifyChecks(response.checks);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'runVerify failed');
    } finally {
      setVerifying(false);
    }
  }, []);

  const signOut = useCallback(async (): Promise<void> => {
    setSaving(true);
    setError(null);
    try {
      await postLogout();
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'signOut failed');
    } finally {
      setSaving(false);
    }
  }, [load]);

  const value = useMemo<DashboardStoreValue>(
    () => ({
      phase,
      me,
      saving,
      error,
      verifying,
      verifyChecks,
      saveSettings,
      saveProject,
      runVerify,
      signOut,
    }),
    [
      phase,
      me,
      saving,
      error,
      verifying,
      verifyChecks,
      saveSettings,
      saveProject,
      runVerify,
      signOut,
    ],
  );

  return <DashboardStoreContext.Provider value={value}>{children}</DashboardStoreContext.Provider>;
}
