// SystemAlertBand tests: the permanent error band renders the server's alert
// detail verbatim (error-agnostic), announces itself to assistive tech, and
// disappears entirely when there is nothing to report.
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { SystemAlertBand } from '@/components/SystemAlertBand';
import { makeSystemAlert } from '@/test/fixtures';

describe('SystemAlertBand', () => {
  it('renders nothing when there are no alerts', () => {
    const { container } = render(<SystemAlertBand alerts={[]} />);
    expect(container.firstChild).toBeNull();
  });

  it('shows each alert detail verbatim and announces the band', () => {
    render(<SystemAlertBand alerts={[makeSystemAlert('2 of 5 sandboxes failing')]} />);
    const band = screen.getByRole('alert');
    expect(band).toHaveTextContent('2 of 5 sandboxes failing');
  });

  it('renders any alert regardless of kind (error-agnostic)', () => {
    render(
      <SystemAlertBand
        alerts={[
          makeSystemAlert('1 of 5 sandboxes failing'),
          makeSystemAlert('something else is broken', 'some_future_kind'),
        ]}
      />,
    );
    expect(screen.getByText('1 of 5 sandboxes failing')).toBeInTheDocument();
    expect(screen.getByText('something else is broken')).toBeInTheDocument();
  });
});
