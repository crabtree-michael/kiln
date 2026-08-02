// The one rendering of a write-only secret's stored state (11 §3 D7), shared
// by every surface that shows one: the Integrations cards' status line and
// modal placeholder, and the project form's sandbox-secret rows.
import type { components } from '@/schema/generated';

type SecretStatus = components['schemas']['SecretStatus'];

/** The exact contract string (task-13 e2e binds to it): "configured · …<tail>". */
export function secretStatusText(status: SecretStatus): string {
  return status.set ? `configured · …${status.tail}` : 'not configured';
}
