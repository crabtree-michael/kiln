// The private-beta screen (`/beta/pending`) — where the GitHub callback lands a
// login the allowlist turned away. Its job is reassurance, so what it must NOT
// say is as load-bearing as what it does.
import { render, screen } from '@testing-library/react';
import { PrivateBeta } from '@/landing/PrivateBeta';

describe('PrivateBeta', () => {
  it('explains the private beta and promises contact', () => {
    render(<PrivateBeta />);

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(/private beta/i);
    expect(screen.getByText(/we'll be in touch/i)).toBeInTheDocument();
  });

  it('asks the visitor for nothing — no form, no next step to chase', () => {
    render(<PrivateBeta />);

    // The whole point of recording the login server-side is that being admitted
    // is our job, not theirs. A form here (or a "go and ask someone" link, which
    // is what the page this replaced used to say) would undo that.
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });

  it('mounts standalone, opening no app shell', () => {
    // It renders outside SessionProvider/SessionGate and any router — everyone
    // who reaches it was just refused a session, so a provider that fetches
    // /api/me would only bounce them off the gate again. Rendering it bare is
    // the assertion: anything requiring context would throw here.
    expect(() => {
      render(<PrivateBeta />);
    }).not.toThrow();
  });
});
