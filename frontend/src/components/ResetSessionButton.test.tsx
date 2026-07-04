import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ResetSessionButton } from '@/components/ResetSessionButton';

// The button is destructive: it must confirm, POST /api/dev/reset, and reload
// only on success. These pin the confirm gate and the reload-on-success wiring.

// jsdom makes location.reload non-configurable, so swap the whole location for
// a plain object carrying a spy — the button only ever reads location.reload.
function stubReload(): ReturnType<typeof vi.fn> {
  const reload = vi.fn();
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: { reload },
  });
  return reload;
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('ResetSessionButton', () => {
  it('does nothing when the confirm dialog is declined', () => {
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const reload = stubReload();

    render(<ResetSessionButton />);
    fireEvent.click(screen.getByRole('button', { name: 'Reset session' }));

    expect(fetchMock).not.toHaveBeenCalled();
    expect(reload).not.toHaveBeenCalled();
  });

  it('posts to /api/dev/reset and reloads on confirm', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    const fetchMock = vi.fn().mockResolvedValue({ ok: true });
    vi.stubGlobal('fetch', fetchMock);
    const reload = stubReload();

    render(<ResetSessionButton />);
    fireEvent.click(screen.getByRole('button', { name: 'Reset session' }));

    await waitFor(() => {
      expect(reload).toHaveBeenCalledTimes(1);
    });
    expect(fetchMock).toHaveBeenCalledWith('/api/dev/reset', { method: 'POST' });
  });

  it('does not reload when the reset request fails', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    const fetchMock = vi.fn().mockResolvedValue({ ok: false });
    vi.stubGlobal('fetch', fetchMock);
    const reload = stubReload();

    render(<ResetSessionButton />);
    fireEvent.click(screen.getByRole('button', { name: 'Reset session' }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalled();
    });
    expect(reload).not.toHaveBeenCalled();
    // Button returns to idle so the developer can retry.
    expect(screen.getByRole('button', { name: 'Reset session' })).toBeEnabled();
  });
});
