// Image-snapshot target (07 §9): `WorkerFree/WorkerTotal` (07 §7).
import type { JSX } from 'react';

export interface CapacityChipProps {
  workerFree: number;
  workerTotal: number;
}

export function CapacityChip({ workerFree, workerTotal }: CapacityChipProps): JSX.Element {
  return (
    <div aria-label="Worker capacity" data-role="capacity-chip">
      {workerFree}/{workerTotal}
    </div>
  );
}
