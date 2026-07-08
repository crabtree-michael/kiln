// Smoke coverage for the marketing landing page (`/landing`): it renders
// standalone (no stores/providers), states the product, funnels its beta CTAs to
// the beta-signup form / #beta anchor (nothing links into the app), points the
// hero "See it anywhere" CTA at the How It Works (#how) section, and embeds the
// captured app screenshots (frontend/public/shots) as themed <picture>/<img>.
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { Landing2 } from '@/landing/Landing2';

function renderLanding(): void {
  render(
    <MemoryRouter>
      <Landing2 />
    </MemoryRouter>,
  );
}

describe('Landing2', () => {
  it('states the product and collects beta emails', () => {
    renderLanding();

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('from anywhere you are');

    // The signup form (hero + closing banner) is the sole conversion surface.
    expect(screen.getAllByLabelText('Email address').length).toBeGreaterThan(0);
    expect(screen.getByRole('button', { name: /notify me/i })).toBeInTheDocument();
  });

  it('funnels beta CTAs to #beta and links nowhere into the app', () => {
    renderLanding();

    // Nav / voice / footer "Join the beta" CTAs all point at #beta, not the app ("/").
    const betaLinks = screen.getAllByRole('link', { name: /join the beta/i });
    expect(betaLinks.length).toBeGreaterThan(0);
    for (const link of betaLinks) {
      expect(link).toHaveAttribute('href', '#beta');
    }
    // No CTA promises to open the app.
    expect(screen.queryByRole('link', { name: /open the app/i })).not.toBeInTheDocument();
  });

  it('routes the hero "See it anywhere" CTA to the How It Works section', () => {
    renderLanding();

    expect(screen.getByRole('link', { name: /see it anywhere/i })).toHaveAttribute('href', '#how');
  });

  it('shows the captured app screenshots (feed, board, dock)', () => {
    renderLanding();

    // The board shot ships a single dark capture; the rest are theme-swapped via
    // <picture>, so their <img> points at the light capture.
    const board = screen.getByRole('img', { name: /the kiln board/i });
    expect(board).toHaveAttribute('src', '/shots/board-dark.png');

    const feed = screen.getAllByRole('img', { name: /activity feed/i });
    expect(feed.length).toBeGreaterThan(0);
    expect(feed[0]).toHaveAttribute('src', '/shots/feed-light.png');

    expect(screen.getByRole('img', { name: /microphone button/i })).toHaveAttribute(
      'src',
      '/shots/dock-light.png',
    );
  });
});
