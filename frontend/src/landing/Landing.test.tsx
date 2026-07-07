// Smoke coverage for the marketing landing page: it renders standalone (no
// stores/providers), states the product, funnels every CTA to the beta-signup
// form (nothing links into the app), and embeds the captured app screenshots
// (frontend/public/shots) as themed <picture>/<img>.
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { Landing } from '@/landing/Landing';

function renderLanding(): void {
  render(
    <MemoryRouter>
      <Landing />
    </MemoryRouter>,
  );
}

describe('Landing', () => {
  it('states the product and collects beta emails', () => {
    renderLanding();

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Manage them by voice');

    // The signup form replaces the old "Open the app" CTA (hero + closing banner).
    expect(screen.getAllByLabelText('Email address').length).toBeGreaterThan(0);
    expect(screen.getByRole('button', { name: /notify me/i })).toBeInTheDocument();
  });

  it('funnels every CTA to the signup form and links nowhere into the app', () => {
    renderLanding();

    // Nav / voice / footer CTAs all point at the #beta anchor, not the app ("/").
    const betaLinks = screen.getAllByRole('link', { name: /join the beta/i });
    expect(betaLinks.length).toBeGreaterThan(0);
    for (const link of betaLinks) {
      expect(link).toHaveAttribute('href', '#beta');
    }
    // No CTA still promises to open the app.
    expect(screen.queryByRole('link', { name: /open the app/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /start talking/i })).not.toBeInTheDocument();
  });

  it('shows the captured app screenshots (feed, board, proposal, dock)', () => {
    renderLanding();

    // The board shot ships a single dark capture; the rest are theme-swapped via
    // <picture>, so their <img> points at the light capture.
    const board = screen.getByRole('img', { name: /the kiln board/i });
    expect(board).toHaveAttribute('src', '/shots/board-dark.png');

    const feed = screen.getAllByRole('img', { name: /activity feed/i });
    expect(feed.length).toBeGreaterThan(0);
    expect(feed[0]).toHaveAttribute('src', '/shots/feed-light.png');

    expect(screen.getByRole('img', { name: /proposal card/i })).toHaveAttribute(
      'src',
      '/shots/proposal-light.png',
    );
    expect(screen.getByRole('img', { name: /microphone button/i })).toHaveAttribute(
      'src',
      '/shots/dock-light.png',
    );
  });
});
