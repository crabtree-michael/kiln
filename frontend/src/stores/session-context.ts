// Split from session.tsx so that file exports only the `SessionProvider`
// component (react-refresh/only-export-components) — this file carries the
// context, its value shape, and the consumer hook.
import type { Me } from '@/transport/transport';
import { createStoreContext } from '@/stores/create-store-context';

/** Where the mount-time `GET /api/me` landed: still in flight (`loading`), no
 * valid session (`signed-out`), or a signed-in account view (`ready`). */
export type SessionStatus = 'loading' | 'signed-out' | 'ready';

export interface SessionStoreValue {
  status: SessionStatus;
  /** Non-null exactly when `status === 'ready'`. `me.project` may still be
   * absent when the user has signed in but not finished dashboard setup. */
  me: Me | null;
}

const { Context: SessionContext, useStore: useSession } =
  createStoreContext<SessionStoreValue>('useSession');

export { SessionContext, useSession };
