// BetaSignupForm behaviour: a valid submit posts the email to the beta list and
// redirects to the confirmation page; a failed post keeps the visitor on the
// form and surfaces an error. transport and useNavigate are mocked so the test
// touches no network or router.
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { BetaSignupForm } from '@/landing/BetaSignupForm';
import { postBetaSignup } from '@/transport/transport';

const navigate = vi.fn();

vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return { ...actual, useNavigate: () => navigate };
});

vi.mock('@/transport/transport', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/transport/transport')>();
  return { ...actual, postBetaSignup: vi.fn() };
});

function renderForm(): void {
  render(
    <MemoryRouter>
      <BetaSignupForm />
    </MemoryRouter>,
  );
}

describe('BetaSignupForm', () => {
  beforeEach(() => {
    vi.mocked(postBetaSignup).mockReset();
    navigate.mockReset();
  });

  it('posts the trimmed email and redirects to the confirmation page', async () => {
    vi.mocked(postBetaSignup).mockResolvedValue(undefined);
    renderForm();

    fireEvent.change(screen.getByLabelText('Email address'), {
      target: { value: '  user@example.com ' },
    });
    fireEvent.click(screen.getByRole('button', { name: /join the beta/i }));

    await waitFor(() => {
      expect(postBetaSignup).toHaveBeenCalledWith('user@example.com');
    });
    expect(navigate).toHaveBeenCalledWith('/beta/thanks');
  });

  it('shows an error and does not redirect when the post fails', async () => {
    vi.mocked(postBetaSignup).mockRejectedValue(new Error('boom'));
    renderForm();

    fireEvent.change(screen.getByLabelText('Email address'), {
      target: { value: 'user@example.com' },
    });
    fireEvent.click(screen.getByRole('button', { name: /join the beta/i }));

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/something went wrong/i);
    });
    expect(navigate).not.toHaveBeenCalled();
  });
});
