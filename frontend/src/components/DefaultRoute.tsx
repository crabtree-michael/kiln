// The site default route (`/`). Browser-tab visitors get the marketing landing
// page; an installed web app is sent straight into the app instead.
//
// This matters most on iOS: a home-screen web app always launches at the
// manifest `start_url` (`/`), so without this redirect an installed iOS app
// would open onto the marketing page every time rather than the board. The
// `/landing` and `/landing-2` aliases stay pinned to the landing page for
// everyone, so there is always an explicit way to reach it.
import type { ReactElement } from 'react';
import { Navigate } from 'react-router-dom';
import { Landing2 } from '@/landing/Landing2';
import { isStandaloneWebApp } from '@/standalone';

export function DefaultRoute(): ReactElement {
  if (isStandaloneWebApp()) {
    return <Navigate to="/app" replace />;
  }
  return <Landing2 />;
}
