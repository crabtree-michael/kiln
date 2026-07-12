// Session gate (11 phase 2): stands between the router and the app screen
// (`/app`) so its data providers — which immediately open SSE and fetch
// board/feed — never mount without a session. Branches on the session
// store's mount-time `GET /api/me`:
//
//   loading    → nothing at all (avoids SSE/feed churn before auth is known)
//   signed-out → full-screen "Continue with GitHub"
//   ready, no project → full-screen pointer to the dashboard's setup flow
//   ready, project    → the app
//
// The sign-in affordance is a plain full-page anchor — NOT a router `Link` —
// because `/auth/github/login` is a backend route the SPA does not own
// (mirrors dashboard/SignIn.tsx). Deliberately self-contained: a minimal
// mobile full-screen card styled inline off the global token sheet, rather
// than importing dashboard components into the app bundle.
import type { CSSProperties, JSX, ReactNode } from 'react';
import { useSession } from '@/stores/session-context';

const screenStyle: CSSProperties = {
  minHeight: '100dvh',
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  justifyContent: 'center',
  gap: 'var(--space-4, 16px)',
  padding: 'var(--page-gutter, 20px)',
  textAlign: 'center',
  background: 'var(--surface-page)',
  color: 'var(--text-body)',
  font: 'var(--type-body)',
};

const wordmarkStyle: CSSProperties = {
  margin: 0,
  font: 'var(--type-display)',
  letterSpacing: 'var(--tracking-display)',
};

const copyStyle: CSSProperties = {
  margin: 0,
  maxWidth: '32ch',
  color: 'var(--text-secondary)',
};

const linkStyle: CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  minHeight: 'var(--touch-target, 44px)',
  padding: '0 var(--space-6, 24px)',
  borderRadius: '999px',
  background: 'var(--accent)',
  color: 'var(--text-on-accent)',
  font: 'var(--type-body-strong)',
  textDecoration: 'none',
};

export interface SessionGateProps {
  children: ReactNode;
}

export function SessionGate({ children }: SessionGateProps): JSX.Element | null {
  const { status, me } = useSession();

  if (status === 'loading') {
    return null;
  }

  if (status === 'signed-out' || me === null) {
    return (
      <div style={screenStyle} data-role="session-gate-signed-out">
        <h1 style={wordmarkStyle}>Kiln</h1>
        <p style={copyStyle}>Sign in to see your board.</p>
        <a href="/auth/github/login" style={linkStyle}>
          Continue with GitHub
        </a>
      </div>
    );
  }

  // Signed in, but `me.project` is absent until the user creates their
  // project on the dashboard (11 §4) — point them there instead of mounting
  // providers that have no project to talk to.
  if (me.project == null) {
    return (
      <div style={screenStyle} data-role="session-gate-no-project">
        <h1 style={wordmarkStyle}>Kiln</h1>
        <p style={copyStyle}>Almost there — connect a project to light the kiln.</p>
        <a href="/dashboard" style={linkStyle}>
          Finish setup on your dashboard
        </a>
      </div>
    );
  }

  return <>{children}</>;
}
