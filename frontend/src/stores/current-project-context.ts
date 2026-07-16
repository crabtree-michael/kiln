// Split from current-project.tsx so that file exports only the provider
// component (react-refresh/only-export-components) — this file carries the
// context, its value shape, and the consumer hook.
import type { MeProject } from '@/transport/transport';
import { createStoreContext } from '@/stores/create-store-context';

/** The client's "current project" (12 §4.1, DP5): the one the app is viewing,
 * the full live set, and a switcher. Projects are referenced by `id`. */
export interface CurrentProjectStoreValue {
  /** The project the app is currently scoped to (null only when the user owns
   * none — the gate keeps that case off the app screen). */
  current: MeProject | null;
  /** The user's live projects, oldest-first (12 §3.1). */
  projects: MeProject[];
  /** Switch the current project by its id (12 §4.1): instant, client-side,
   * localStorage-persisted (MRU). */
  selectProject: (id: string) => void;
}

const { Context: CurrentProjectContext, useStore: useCurrentProject } =
  createStoreContext<CurrentProjectStoreValue>('useCurrentProject');

export { CurrentProjectContext, useCurrentProject };
