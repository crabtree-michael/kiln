// Split from dashboard-store.tsx so that file exports only the
// `DashboardProvider` component (react-refresh/only-export-components) —
// this file carries the account-view shape, the context, and the consumer
// hook (11 §5, §9), mirroring `stores/chat-context.ts`.
import { createStoreContext } from '@/stores/create-store-context';
import type { Me, ProjectUpdateRequest, SettingsUpdateRequest, VerifyCheck } from '@/transport/transport';

/**
 * `loading`: the initial `GET /api/me` is in flight (mount, or right after
 * `signOut`'s re-fetch).
 * `signed-out`: `fetchMe` resolved `null` (no valid session) — the dashboard
 * renders its signed-out view.
 * `ready`: `me` is populated; the dashboard renders the account view.
 */
export type DashboardPhase = 'loading' | 'signed-out' | 'ready';

export interface DashboardStoreValue {
  phase: DashboardPhase;
  /** The signed-in user's account view; `null` until loaded or when signed out. */
  me: Me | null;
  /** `true` while a settings/project save or a sign-out is in flight. */
  saving: boolean;
  /** The most recent action's failure message, if any; cleared on the next attempt. */
  error: string | null;
  /** `true` while `POST /api/settings/verify` is in flight. */
  verifying: boolean;
  /** The most recent verify run's per-check results; `null` until one has run. */
  verifyChecks: VerifyCheck[] | null;
  /** `PUT /api/settings`, then swaps `me` for the response. */
  saveSettings: (body: SettingsUpdateRequest) => Promise<void>;
  /** `PUT /api/project`, then swaps `me` for the response. */
  saveProject: (body: ProjectUpdateRequest) => Promise<void>;
  /** `POST /api/settings/verify`, populating `verifyChecks` from the response. */
  runVerify: () => Promise<void>;
  /** `POST /auth/logout`, then re-fetches `/api/me` to land on `signed-out`. */
  signOut: () => Promise<void>;
}

const { Context: DashboardStoreContext, useStore: useDashboardStore } =
  createStoreContext<DashboardStoreValue>('useDashboardStore');

export { DashboardStoreContext, useDashboardStore };
