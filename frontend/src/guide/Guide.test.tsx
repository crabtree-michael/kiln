// Smoke coverage for the onboarding guide page (`/guide`): it renders standalone
// (no stores/providers), leads with the guide's title, structures the three
// parts as headings, renders the credential checklist and board-state tables,
// and links into the app (dashboard) rather than embedding it.
import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { Guide } from '@/guide/Guide';

function renderGuide(): void {
  render(
    <MemoryRouter>
      <Guide />
    </MemoryRouter>,
  );
}

describe('Guide', () => {
  it('leads with the guide title and the three parts as sections', () => {
    renderGuide();

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(
      'Getting started with Kiln',
    );

    for (const title of [
      'Setting up Kiln',
      'Using Kiln day to day',
      'How Kiln works under the hood',
    ]) {
      expect(screen.getByRole('heading', { level: 2, name: title })).toBeInTheDocument();
    }
  });

  it('walks through the six numbered setup steps', () => {
    renderGuide();

    for (const step of [
      'Sign in with GitHub',
      'Create your project',
      'Add and verify your credentials',
      /Turn on notifications/,
      'Open Kiln on your phone',
      /Install Kiln on your iPhone/,
    ]) {
      expect(screen.getByRole('heading', { level: 3, name: step })).toBeInTheDocument();
    }
  });

  it('renders the credentials checklist with the external key sources', () => {
    renderGuide();

    expect(screen.getByRole('link', { name: /console\.anthropic\.com/i })).toHaveAttribute(
      'href',
      'https://console.anthropic.com',
    );
  });

  it('tabulates the five board states', () => {
    renderGuide();

    for (const state of ['shaping', 'ready', 'working', 'blocked', 'done']) {
      // Each state appears in the board-states table; assert the code cell exists.
      expect(screen.getAllByText(state).length).toBeGreaterThan(0);
    }
  });

  it('links into the app (dashboard) rather than embedding it', () => {
    renderGuide();

    const openLinks = screen.getAllByRole('link', { name: /open kiln/i });
    expect(openLinks.length).toBeGreaterThan(0);
    expect(openLinks[0]).toHaveAttribute('href', '/dashboard');

    // The table of contents links to on-page anchors, not app routes.
    const toc = within(screen.getByRole('banner')).getAllByRole('link', {
      name: /how kiln works/i,
    });
    expect(toc[0]).toHaveAttribute('href', '#how');
  });
});
