// The landing page's "Join the beta" form. A single email field + submit that
// posts to the beta list (transport.postBetaSignup) and, on success, redirects
// to the standalone confirmation page (/beta/thanks). Stateless beyond its own
// input/pending/error — the landing page holds no app state, and neither does
// this (07: the marketing page opens no stream/store/provider).
import { useState, type FormEvent, type JSX } from 'react';
import { useNavigate } from 'react-router-dom';
import { postBetaSignup } from '@/transport/transport';

/** Where a successful signup lands — the reassurance page (no app chrome). */
const CONFIRMATION_PATH = '/beta/thanks';

export function BetaSignupForm({
  cta = 'Join the beta',
  autoFocus = false,
}: {
  cta?: string;
  /** Focus the email field on mount — used when the form opens inside the beta
   * modal so the user can start typing immediately. */
  autoFocus?: boolean;
}): JSX.Element {
  const navigate = useNavigate();
  const [email, setEmail] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (pending) return;
    setPending(true);
    setError(null);
    try {
      await postBetaSignup(email.trim());
      void navigate(CONFIRMATION_PATH);
    } catch {
      setPending(false);
      setError('Something went wrong. Please try again.');
    }
  }

  return (
    <form
      className="kiln-beta-form"
      onSubmit={(event) => {
        void handleSubmit(event);
      }}
      noValidate={false}
      aria-label="Join the beta"
    >
      <div className="kiln-beta-form__row">
        <input
          type="email"
          className="kiln-beta-form__input"
          placeholder="you@example.com"
          value={email}
          onChange={(event) => {
            setEmail(event.target.value);
          }}
          required
          autoComplete="email"
          aria-label="Email address"
          disabled={pending}
          autoFocus={autoFocus}
        />
        <button
          type="submit"
          className="kiln-btn kiln-btn--primary kiln-btn--lg"
          disabled={pending}
        >
          {pending ? 'Joining…' : cta}
        </button>
      </div>
      {error !== null && (
        <p className="kiln-beta-form__error" role="alert">
          {error}
        </p>
      )}
    </form>
  );
}
