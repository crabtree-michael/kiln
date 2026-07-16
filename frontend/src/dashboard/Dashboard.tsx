// The `/dashboard/*` route (11 §5): owns its own `DashboardProvider` (the app
// shell at `/` never mounts it, so nothing else needs the account view) and
// switches on the store's `phase` — loading spinner, sign-in, onboarding, or
// the full settings view. `data-role="dashboard"` is the root the task-13 e2e
// scopes every other selector under.
import type { JSX } from 'react';
import { DashboardProvider } from '@/dashboard/dashboard-store';
import { useDashboardStore } from '@/dashboard/dashboard-context';
import { SignIn } from '@/dashboard/SignIn';
import { Onboarding } from '@/dashboard/Onboarding';
import { Settings } from '@/dashboard/Settings';
import '@/dashboard/Dashboard.css';

function DashboardBody(): JSX.Element {
  const { phase, me } = useDashboardStore();

  if (phase === 'signed-out') {
    return <SignIn />;
  }

  if (phase === 'loading' || me === null) {
    // The `me === null` arm only guards `phase === 'ready'` against a store
    // contract violation (ready always pairs with a populated `me`) — it
    // narrows the type below without an `as`.
    return (
      <div data-role="dashboard-loading">
        <span data-role="dashboard-spinner" />
        Loading…
      </div>
    );
  }

  // "Not onboarded" is now projects.length === 0 (12 §4.1): no projects → the
  // onboarding form; one or more → the project-list settings view.
  return me.projects.length === 0 ? <Onboarding /> : <Settings />;
}

export function Dashboard(): JSX.Element {
  return (
    <DashboardProvider>
      <div data-role="dashboard">
        <DashboardBody />
      </div>
    </DashboardProvider>
  );
}
