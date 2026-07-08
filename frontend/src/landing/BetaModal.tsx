// The "Join the beta" flow as a focused modal (replaces the old scroll-to-#beta
// section at the bottom of the marketing page). Any "Join the beta" trigger in
// the nav, voice section, or footer now opens this centered dialog instead of scrolling the page,
// so signing up reads as a deliberate action rather than a trip to the footer.
//
// It renders nothing when closed, so the wrapped BetaSignupForm fully resets
// between opens. While open it locks body scroll, closes on Escape or a
// backdrop click, and focuses the email field. The email-capture logic itself
// (POST + redirect to /beta/thanks) lives unchanged in BetaSignupForm — this is
// pure presentation, matching the standalone, stateless nature of the pages.
import { useEffect, type JSX } from 'react';
import { BetaSignupForm } from '@/landing/BetaSignupForm';
import '@/landing/BetaModal.css';

const TITLE_ID = 'kiln-beta-modal-title';

export function BetaModal({
  open,
  onClose,
  heading,
  blurb,
}: {
  open: boolean;
  onClose: () => void;
  /** Panel headline — each page keeps its own voice (see the landing pages). */
  heading: string;
  /** Supporting line under the headline. */
  blurb: string;
}): JSX.Element | null {
  useEffect(() => {
    if (!open) return undefined;
    function onKeyDown(event: KeyboardEvent): void {
      if (event.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', onKeyDown);
    // Freeze the page behind the modal so the backdrop reads as a real overlay.
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.removeEventListener('keydown', onKeyDown);
      document.body.style.overflow = previousOverflow;
    };
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div
      className="kiln-beta-modal"
      onMouseDown={(event) => {
        // Dismiss only on a click that both starts and ends on the backdrop —
        // a drag that begins inside the panel (e.g. selecting text) must not
        // close it.
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div
        className="kiln-beta-modal__panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby={TITLE_ID}
      >
        <button
          type="button"
          className="kiln-beta-modal__close"
          aria-label="Close"
          onClick={onClose}
        >
          <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" focusable="false">
            <path
              d="M3 3 L13 13 M13 3 L3 13"
              stroke="currentColor"
              strokeWidth="1.6"
              strokeLinecap="round"
            />
          </svg>
        </button>
        <h2 id={TITLE_ID} className="kiln-beta-modal__title">
          {heading}
        </h2>
        <p className="kiln-beta-modal__blurb">{blurb}</p>
        <BetaSignupForm cta="Notify me" autoFocus />
      </div>
    </div>
  );
}
