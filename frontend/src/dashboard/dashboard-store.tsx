// Dashboard store (11 §5, §9): loads the signed-in `GET /api/me` account view
// on mount, and drives the settings/project save, connectivity verify, and
// sign-out actions — a straight transposition of the chat-store pattern
// (`useState` for the value pieces, `useCallback` actions, `useEffect`
// mount-load, `useMemo` context value). `saveSettings` auto-chains a verify
// run after any successful credential-field save (dashboard UX update:
// per-field auto-save + auto-verify, superseding the old manual "Save
// credentials" / "Test connections" buttons); `saveProject` never does.
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
  type CredentialName,
  type DashboardPhase,
  type DashboardStoreValue,
} from '@/dashboard/dashboard-context';

/** The `SettingsUpdateRequest` keys that are write-only secrets with their own
 * verify check and indicator — as opposed to `amika_claude_cred_id`, which is
 * plain text and never chains a verify run. */
const CREDENTIAL_KEYS: readonly CredentialName[] = [
  'anthropic_api_key',
  'amika_api_key',
  'github_auth_token',
];

/** Which single credential field (if any) a partial `SettingsUpdateRequest`
 * body is writing — each auto-save commits exactly one field, so at most one
 * ever matches. */
function credentialKeyIn(body: SettingsUpdateRequest): CredentialName | null {
  for (const key of CREDENTIAL_KEYS) {
    const value = body[key];
    if (typeof value === 'string' && value !== '') {
      return key;
    }
  }
  return null;
}

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
  const [pendingCredential, setPendingCredential] = useState<CredentialName | null>(null);

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

  // Auto-save + auto-verify (dashboard UX update): each credential input
  // commits its own partial body on blur/Enter, with no submit button at all.
  // A save that touches one of the three secret fields automatically chains
  // straight into `runVerify` on success, so the field's right-of-input
  // indicator reflects a fresh result without a manual "test connections"
  // step. `pendingCredential` stays set across both the save and (when
  // chained) the verify, so the indicator reads "pending" for that whole
  // window; `saving` itself only covers the save call, matching its existing
  // use for the project/sign-out buttons.
  const saveSettings = useCallback(
    async (body: SettingsUpdateRequest): Promise<boolean> => {
      const credentialKey = credentialKeyIn(body);
      if (credentialKey !== null) {
        setPendingCredential(credentialKey);
      }
      setSaving(true);
      setError(null);
      let succeeded = false;
      try {
        const updated = await putSettings(body);
        setMe(updated);
        succeeded = true;
      } catch (err) {
        setError(err instanceof Error ? err.message : 'saveSettings failed');
      } finally {
        setSaving(false);
      }
      if (succeeded && credentialKey !== null) {
        await runVerify();
      }
      if (credentialKey !== null) {
        setPendingCredential(null);
      }
      return succeeded;
    },
    [runVerify],
  );

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
      pendingCredential,
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
      pendingCredential,
      saveSettings,
      saveProject,
      runVerify,
      signOut,
    ],
  );

  return <DashboardStoreContext.Provider value={value}>{children}</DashboardStoreContext.Provider>;
}
