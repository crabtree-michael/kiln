// CapacityChip image-snapshot target (07 §9): WorkerFree/WorkerTotal (07 §7).
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { CapacityChip } from '@/components/CapacityChip';

describe('CapacityChip', () => {
  it('renders workerFree/workerTotal derived from the board snapshot', () => {
    render(<CapacityChip workerFree={2} workerTotal={5} />);

    expect(screen.getByLabelText('Worker capacity')).toHaveTextContent('2/5');
  });

  it('renders zero free capacity distinctly from full capacity', () => {
    render(<CapacityChip workerFree={0} workerTotal={5} />);

    expect(screen.getByLabelText('Worker capacity')).toHaveTextContent('0/5');
  });

  it('matches the DOM-structure snapshot (07 §9 target)', () => {
    const { container } = render(<CapacityChip workerFree={3} workerTotal={4} />);

    expect(container).toMatchSnapshot();
  });
});
