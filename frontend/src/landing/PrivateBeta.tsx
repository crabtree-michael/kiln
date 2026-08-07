// The private-beta screen (`/beta/pending`). Reached exactly one way: a visitor
// completed GitHub auth and the allowlist (11 §2) turned them away, so the
// backend callback recorded their login on the beta list and redirected here.
//
// It is therefore NOT an error page, and must not read as one — the person did
// everything right and is simply early. The two things it owes them are the two
// they cannot work out for themselves: that access is gated at all, and that
// being let in is something we do rather than something they must chase.
//
// Deliberately self-contained: it mounts no app shell, opens no
// stream/store/provider, and links nowhere into the app — a visitor with no
// session would only bounce off the gate. It borrows only the Kiln design tokens
// (src/styles/tokens.css) for colour/typography, so it follows the OS theme like
// the marketing page. It is a public route for that reason: it sits OUTSIDE
// SessionProvider/SessionGate in main.tsx, since its whole audience is people
// without a session.
//
// (This screen and its stylesheet were the old `/beta/thanks` confirmation for
// the retired email-capture form. The reassurance it gives is the same one that
// flow ended on, so it was repointed rather than rewritten.)
import type { JSX } from 'react';
import '@/landing/PrivateBeta.css';

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

export function PrivateBeta(): JSX.Element {
  return (
    <main className="kiln-thanks">
      <div className="kiln-thanks__inner">
        <BellMark />
        <h1 className="kiln-thanks__title">Kiln is in private beta.</h1>
        <p className="kiln-thanks__lead">
          Thanks for signing up — your GitHub account is on the list. We're letting people in a few
          at a time, and we'll be in touch as soon as your spot is ready. There's nothing else you
          need to do.
        </p>
      </div>
    </main>
  );
}
