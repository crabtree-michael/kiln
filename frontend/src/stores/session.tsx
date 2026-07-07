// Session store (11 phase 2): one `GET /api/me` on mount, resolved into a
// three-way status the `SessionGate` can branch on before the app's data
// providers mount (they immediately open SSE + fetch board/feed, which would
// all 401 without a session). Mirrors dashboard-store's mount-load shape —
// `fetchMe` resolves `null` for the ordinary signed-out 401; anything else
// (a 500, a network blip) rejects and is folded into `signed-out` so the
// gate never spins forever.
import { useEffect, useMemo, useState, type JSX, type ReactNode } from 'react';
import { fetchMe } from '@/transport/transport';
import type { Me } from '@/transport/transport';
import {
  SessionContext,
  type SessionStatus,
  type SessionStoreValue,
} from '@/stores/session-context';

export interface SessionProviderProps {
  children: ReactNode;
}

export function SessionProvider({ children }: SessionProviderProps): JSX.Element {
  const [status, setStatus] = useState<SessionStatus>('loading');
  const [me, setMe] = useState<Me | null>(null);

  useEffect(() => {
    let cancelled = false;
    // Named (not an IIFE) so the `cancelled` checks see the flag's declared
    // type rather than its always-false value at creation time
    // (@typescript-eslint/no-unnecessary-condition) — dashboard-store's
    // mount-load shape.
    const load = async (): Promise<void> => {
      try {
        const account = await fetchMe();
        if (cancelled) {
          return;
        }
        setMe(account);
        setStatus(account === null ? 'signed-out' : 'ready');
      } catch {
        if (cancelled) {
          return;
        }
        setMe(null);
        setStatus('signed-out');
      }
    };
    void load();
    return () => {
      cancelled = true;
    };
  }, []);

  const value = useMemo<SessionStoreValue>(() => ({ status, me }), [status, me]);

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}
