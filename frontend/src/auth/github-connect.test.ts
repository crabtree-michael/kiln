import { describe, it, expect } from 'vitest';
import { GITHUB_CONNECT_PATH } from '@/auth/github-connect';

// This constant is one half of a contract the type system can't see: the other
// half is `GET /auth/github/connect` in backend/internal/api/routes.go. Pinning
// the literal is what makes a rename over there fail here rather than in a
// browser.
describe('GITHUB_CONNECT_PATH', () => {
  it('is the backend route that starts the repo-scoped grant', () => {
    expect(GITHUB_CONNECT_PATH).toBe('/auth/github/connect');
  });

  it('is a backend path, so navigation to it leaves the SPA', () => {
    // Not a router path: nothing in the client's route table serves it, and a
    // router `Link` would swallow the navigation instead of hitting the server.
    expect(GITHUB_CONNECT_PATH.startsWith('/auth/')).toBe(true);
  });
});
