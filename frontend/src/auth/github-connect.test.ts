import { describe, it, expect } from 'vitest';
import {
  GITHUB_CONNECT_PATH,
  GITHUB_DASHBOARD_RETURN_PATH,
  GITHUB_SETUP_PATH,
} from '@/auth/github-connect';

// These constants are one half of a contract the type system can't see: the
// other half is `GET /auth/github/connect` in backend/internal/api/routes.go,
// and the `setup` and `next` query parameters its handler reads. Pinning the
// literals is what makes a rename over there fail here rather than in a browser.
describe('GITHUB_CONNECT_PATH', () => {
  it('is the backend route that starts the one GitHub grant', () => {
    expect(GITHUB_CONNECT_PATH).toBe('/auth/github/connect');
  });

  it('is a backend path, so navigation to it leaves the SPA', () => {
    // Not a router path: nothing in the client's route table serves it, and a
    // router `Link` would swallow the navigation instead of hitting the server.
    expect(GITHUB_CONNECT_PATH.startsWith('/auth/')).toBe(true);
  });
});

describe('GITHUB_DASHBOARD_RETURN_PATH', () => {
  // `next` names where the flow ENDS, not where it goes: without it a completed
  // sign-in lands in the app, which is what someone clicking "Sign in" wants and
  // what the dashboard's own affordances do not.
  it('is the same route asking to come back to the dashboard', () => {
    expect(GITHUB_DASHBOARD_RETURN_PATH).toBe('/auth/github/connect?next=dashboard');
    expect(GITHUB_DASHBOARD_RETURN_PATH.startsWith(GITHUB_CONNECT_PATH)).toBe(true);
  });
});

describe('GITHUB_SETUP_PATH', () => {
  // The same route with the flag the handler branches on — one route, one
  // rename, so the two can never drift apart.
  it('is the same route asking for GitHub’s repository chooser', () => {
    expect(GITHUB_SETUP_PATH).toBe('/auth/github/connect?setup=1');
    expect(GITHUB_SETUP_PATH.startsWith(GITHUB_CONNECT_PATH)).toBe(true);
  });
});
