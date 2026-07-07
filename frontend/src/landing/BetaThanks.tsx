// Standalone beta-signup confirmation page (`/beta/thanks`). Reached only after
// a successful "Join the beta" submit. Deliberately self-contained: it mounts no
// app shell, opens no stream/store/provider, and links nowhere into the app —
// its whole job is to reassure the visitor we'll be in touch. It borrows only
// the Kiln design tokens (src/styles/tokens.css) for colour/typography, so it
// follows the OS theme like the marketing page.
import type { JSX } from 'react';
import '@/landing/BetaThanks.css';

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

export function BetaThanks(): JSX.Element {
  return (
    <main className="kiln-thanks">
      <div className="kiln-thanks__inner">
        <BellMark />
        <h1 className="kiln-thanks__title">You're on the list.</h1>
        <p className="kiln-thanks__lead">
          Thanks for your interest in Kiln. The beta is still warming up — we'll email you as soon
          as it's ready for you. No spam in the meantime.
        </p>
      </div>
    </main>
  );
}
