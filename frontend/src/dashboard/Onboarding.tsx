// First-run view (11 §5): the signed-in user has no project yet. Just the
// project step — no wizard machinery. Once `saveProject` succeeds the store's
// refreshed `me.project` is non-nil and `Dashboard` itself swaps this out for
// `Settings` (credentials + verify live there); this component never tracks
// "did I just save" locally.
import type { JSX } from 'react';
import { useDashboardStore } from '@/dashboard/dashboard-context';
import { ProjectFields } from '@/dashboard/ConfigFields';

export function Onboarding(): JSX.Element {
  const { me, saveProject, saving, error } = useDashboardStore();
  if (me === null) {
    // Dashboard only mounts this view once `phase` is 'ready', which the store
    // contract pairs with a populated `me` — this guard just lets TS narrow
    // without an escape hatch (should never actually trip in practice).
    throw new Error('Onboarding rendered without a signed-in account');
  }

  return (
    <div data-role="onboarding">
      <h1>Set up your project</h1>
      <ProjectFields providers={me.providers ?? []} saving={saving} onSave={saveProject} />
      {error !== null ? (
        <p data-role="dashboard-error" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  );
}
