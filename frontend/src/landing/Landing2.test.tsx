// Smoke coverage for the marketing landing page (the default `/` route, also at
// `/landing`): it renders standalone (no stores/providers), states the product,
// funnels its beta CTAs to the beta-signup modal, offers a GitHub sign-in beside
// them, points its "How it works" CTAs at the How It Works (#how) section, and
// embeds the captured app screenshots (frontend/public/shots) as themed
// <picture>/<img>.
import { fireEvent, render, screen, within } from '@testing-library/react';
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

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(
      /shipped a whole Pac-Man with a team of agents/i,
    );

    // The hero embeds the signup form inline; the closing banner is gone, so its
    // "Notify me" submit now lives in the beta modal and is absent until a CTA
    // opens it.
    expect(screen.getAllByLabelText('Email address').length).toBeGreaterThan(0);
    expect(screen.queryByRole('button', { name: /notify me/i })).not.toBeInTheDocument();

    // Opening a CTA reveals the modal dialog with the signup form.
    fireEvent.click(
      within(screen.getByRole('banner')).getByRole('button', { name: /join the beta/i }),
    );
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getByLabelText('Email address')).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: /notify me/i })).toBeInTheDocument();
  });

  it('funnels beta CTAs to the signup modal and offers a GitHub sign-in', () => {
    renderLanding();

    // The modal is closed until a CTA opens it — no dialog up front.
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    // Nav / voice / footer "Join the beta" CTAs are buttons (they open the
    // modal), not links into the app.
    const betaCtas = screen.getAllByRole('button', { name: /join the beta/i });
    expect(betaCtas.length).toBeGreaterThan(0);
    expect(screen.queryByRole('link', { name: /join the beta/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /open the app/i })).not.toBeInTheDocument();

    // The sign-in affordance sits beside the beta CTA and is a plain full-page
    // anchor to the backend-owned GitHub OAuth start (not a router Link into the
    // SPA), mirroring SessionGate / dashboard SignIn.
    const signIn = screen.getByRole('link', { name: /sign in/i });
    expect(signIn).toHaveAttribute('href', '/auth/github/login');

    // Closing the opened modal returns to the page.
    fireEvent.click(
      within(screen.getByRole('banner')).getByRole('button', { name: /join the beta/i }),
    );
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /close/i }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('routes every "How it works" link (nav, hero, footer) to the #how section', () => {
    renderLanding();

    const links = screen.getAllByRole('link', { name: /how it works/i });
    expect(links.length).toBeGreaterThan(0);
    for (const link of links) {
      expect(link).toHaveAttribute('href', '#how');
    }
  });

  it('shows the captured app screenshots (hero pacman feed, feature feed, board, dock)', () => {
    renderLanding();

    // The hero is the finished-Pac-Man feed (all-✅), a distinct shot from the
    // feature-section feed below it — both alt texts start "The Kiln activity
    // feed", so match each on the part unique to it.
    const hero = screen.getByRole('img', { name: /Pac-Man build/i });
    expect(hero).toHaveAttribute('src', '/shots/pacman-light.png');

    const feed = screen.getByRole('img', { name: /blocker pinned/i });
    expect(feed).toHaveAttribute('src', '/shots/feed-light.png');

    const board = screen.getByRole('img', { name: /the kiln board/i });
    expect(board).toHaveAttribute('src', '/shots/board-dark.png');

    expect(screen.getByRole('img', { name: /microphone button/i })).toHaveAttribute(
      'src',
      '/shots/dock-light.png',
    );
  });
});
