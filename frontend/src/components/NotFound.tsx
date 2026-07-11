// Catch-all 404 page for unmatched routes (registered as `path="*"` in
// main.tsx, last so every real route wins first). Deliberately self-contained
// like BetaThanks: it mounts no app shell, opens no stream/store/provider, and
// borrows only the Kiln design tokens (src/styles/tokens.css) so light/dark
// follows the OS theme. Its one job is to tell the visitor the page was not
// found and hand them a way back to the app, rather than the blank screen an
// unmatched route rendered before.
import type { JSX } from 'react';
import { Link } from 'react-router-dom';
import '@/components/NotFound.css';

/** The Kiln bell mark, inlined so the page depends on no shared component. */
function BellMark(): JSX.Element {
  return (
    <svg width={44} height={44} viewBox="0 0 96 96" aria-hidden="true" focusable="false">
      <path
        d="M48 12 C33 12 25 25 25 43 C25 56 21 63 16.5 67.5 C14.5 69.7 16 74 19.5 74 H76.5 C80 74 81.5 69.7 79.5 67.5 C75 63 71 56 71 43 C71 25 63 12 48 12 Z"
        fill="var(--accent)"
      />
      <path d="M39 78 A9 9 0 0 0 57 78 Z" fill="var(--accent)" />
    </svg>
  );
}

export function NotFound(): JSX.Element {
  return (
    <main className="kiln-notfound">
      <div className="kiln-notfound__inner">
        <BellMark />
        <p className="kiln-notfound__code">404</p>
        <h1 className="kiln-notfound__title">Page not found</h1>
        <p className="kiln-notfound__lead">
          The page you were looking for doesn't exist or may have moved.
        </p>
        <Link to="/" className="kiln-notfound__home">
          Back to Kiln
        </Link>
      </div>
    </main>
  );
}
