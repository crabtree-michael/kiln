// Smoke coverage for the marketing landing page: it renders standalone (no
// stores/providers), states the product, links into the app, and embeds the
// captured app screenshots (frontend/public/shots) as themed <picture>/<img>.
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
  it('states the product and links into the app', () => {
    renderLanding();

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Manage them by voice');

    const openLinks = screen.getAllByRole('link', { name: /open the app/i });
    expect(openLinks.length).toBeGreaterThan(0);
    expect(openLinks[0]).toHaveAttribute('href', '/');
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
