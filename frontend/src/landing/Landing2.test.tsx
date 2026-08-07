// Smoke coverage for the marketing landing page (the default `/` route, also at
// `/landing`): it renders standalone (no stores/providers), states the product,
// offers Sign up and Log in as two buttons through the one GitHub flow, points
// its "How it works" CTAs at the How It Works (#how) section, and embeds the
// captured app screenshots (frontend/public/shots) as themed <picture>/<img>.
import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { Landing2 } from '@/landing/Landing2';
import { GITHUB_CONNECT_PATH } from '@/auth/github-connect';

function renderLanding(): void {
  render(
    <MemoryRouter>
      <Landing2 />
    </MemoryRouter>,
  );
}

describe('Landing2', () => {
  it('states the product and names the private beta up front', () => {
    renderLanding();

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(
      /Orchestrate a team of coding agents from anywhere you are/i,
    );

    // The gate is stated on the page rather than discovered at the end of a
    // GitHub round trip.
    expect(screen.getByText(/Kiln is in private beta/i)).toBeInTheDocument();
  });

  it('offers Sign up and Log in as two separate buttons in the nav', () => {
    renderLanding();

    const nav = within(screen.getByRole('banner'));
    const signUp = nav.getByRole('link', { name: 'Sign up' });
    const logIn = nav.getByRole('link', { name: 'Log in' });

    // Two distinct affordances...
    expect(signUp).not.toBe(logIn);
    // ...through ONE flow (11 D2a): same backend route, differing in wording
    // only. A second GitHub path here is the exact mistake that decision closed.
    expect(signUp).toHaveAttribute('href', GITHUB_CONNECT_PATH);
    expect(logIn).toHaveAttribute('href', GITHUB_CONNECT_PATH);
  });

  it('sends every auth CTA through the one GitHub route, as full-page anchors', () => {
    renderLanding();

    // Nav, hero and voice/footer CTAs alike. They must be links (a real
    // navigation to a backend route the SPA does not own), never buttons — a
    // router Link or an onClick handler would keep the SPA mounted and never
    // reach the OAuth start.
    const authLinks = screen
      .getAllByRole('link')
      .filter((link) => link.getAttribute('href') === GITHUB_CONNECT_PATH);
    expect(authLinks.length).toBeGreaterThanOrEqual(4);
  });

  it('no longer collects an email anywhere — signing up IS the GitHub grant', () => {
    renderLanding();

    // The old form + modal are gone: no email field, no "Join the beta" trigger,
    // and nothing that opens a dialog.
    expect(screen.queryByLabelText('Email address')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /join the beta/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /join the beta/i })).not.toBeInTheDocument();
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

  it('shows the captured app screenshots (hero pacman feed, feature feed, dock)', () => {
    renderLanding();

    // The hero is the finished-Pac-Man feed (all-✅), a distinct shot from the
    // feature-section feed below it — both alt texts start "The Kiln activity
    // feed", so match each on the part unique to it.
    const hero = screen.getByRole('img', { name: /Pac-Man build/i });
    expect(hero).toHaveAttribute('src', '/shots/pacman-light.png');

    const feed = screen.getByRole('img', { name: /blocker pinned/i });
    expect(feed).toHaveAttribute('src', '/shots/feed-light.png');

    expect(screen.getByRole('img', { name: /microphone button/i })).toHaveAttribute(
      'src',
      '/shots/dock-light.png',
    );
  });
});
