// The site default route (`/`): the marketing landing page in a browser tab,
// but a redirect to `/app` when launched as an installed standalone web app
// (an iOS home-screen app most of all, whose start_url is pinned to `/`).
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { DefaultRoute } from '@/components/DefaultRoute';

// Renders `/` through the real router so a redirect actually resolves to the
// `/app` route. The app screen is stubbed so this test stays free of its data
// providers (SSE/board fetch); we only assert which route won.
function renderDefault(): void {
  render(
    <MemoryRouter initialEntries={['/']}>
      <Routes>
        <Route path="/" element={<DefaultRoute />} />
        <Route path="/app" element={<div data-testid="app-screen">app</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('DefaultRoute', () => {
  afterEach(() => {
    Reflect.deleteProperty(navigator, 'standalone');
    vi.unstubAllGlobals();
  });

  it('renders the marketing landing page for browser-tab visitors', () => {
    // jsdom has no `navigator.standalone` and no `matchMedia`, i.e. a plain tab.
    renderDefault();

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('from anywhere you are');
    expect(screen.queryByTestId('app-screen')).not.toBeInTheDocument();
  });

  it('redirects an installed iOS home-screen web app to the app', () => {
    // iOS Safari sets `navigator.standalone` only for home-screen launches.
    Object.defineProperty(navigator, 'standalone', { configurable: true, value: true });
    renderDefault();

    expect(screen.getByTestId('app-screen')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { level: 1 })).not.toBeInTheDocument();
  });

  it('redirects an installed (display-mode: standalone) web app to the app', () => {
    // The web-standard signal for installed Chrome/Edge/Android web apps.
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: query === '(display-mode: standalone)',
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }));
    renderDefault();

    expect(screen.getByTestId('app-screen')).toBeInTheDocument();
  });
});
