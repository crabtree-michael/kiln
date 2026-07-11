// Tests for the notification-frequency hook (02 §10). The transport module is
// mocked at its boundary; the hook's job is the initial read, the optimistic
// write, and the rollback on failure.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { JSX } from 'react';
import { useNotificationMode } from '@/stores/use-notification-mode';
import * as transport from '@/transport/transport';

vi.mock('@/transport/transport', () => ({
  fetchNotificationMode: vi.fn(),
  putNotificationMode: vi.fn(),
}));

const fetchMock = vi.mocked(transport.fetchNotificationMode);
const putMock = vi.mocked(transport.putNotificationMode);

function Probe(): JSX.Element {
  const { mode, ready, setMode } = useNotificationMode();
  return (
    <div>
      <span data-testid="mode">{mode}</span>
      <span data-testid="ready">{ready ? 'ready' : 'loading'}</span>
      <button
        type="button"
        onClick={() => {
          setMode('all');
        }}
      >
        all
      </button>
      <button
        type="button"
        onClick={() => {
          setMode('blocked');
        }}
      >
        blocked
      </button>
    </div>
  );
}

afterEach(() => {
  vi.clearAllMocks();
});

describe('useNotificationMode', () => {
  it('reads the current mode on mount', async () => {
    fetchMock.mockResolvedValue('all');
    render(<Probe />);
    await waitFor(() => {
      expect(screen.getByTestId('ready')).toHaveTextContent('ready');
    });
    expect(screen.getByTestId('mode')).toHaveTextContent('all');
  });

  // Regression guard for the cross-device sync ticket: the shared mode is a
  // per-user global (one value for all of a user's devices). Merely opening the
  // app on a second device must never write it — otherwise a device that read a
  // stale/default value could clobber the mode another device just set. Mount
  // reads, and only an explicit selection writes.
  it('never writes the mode on mount (read-only launch)', async () => {
    fetchMock.mockResolvedValue('all');
    render(<Probe />);
    await waitFor(() => {
      expect(screen.getByTestId('ready')).toHaveTextContent('ready');
    });
    expect(putMock).not.toHaveBeenCalled();
  });

  it('falls back to blocked when the read fails', async () => {
    fetchMock.mockRejectedValue(new Error('offline'));
    render(<Probe />);
    await waitFor(() => {
      expect(screen.getByTestId('ready')).toHaveTextContent('ready');
    });
    expect(screen.getByTestId('mode')).toHaveTextContent('blocked');
  });

  it('optimistically applies a selection and persists it', async () => {
    fetchMock.mockResolvedValue('blocked');
    putMock.mockResolvedValue('all');
    render(<Probe />);
    await waitFor(() => {
      expect(screen.getByTestId('ready')).toHaveTextContent('ready');
    });

    fireEvent.click(screen.getByText('all'));
    // Optimistic: reflected before the PUT resolves.
    expect(screen.getByTestId('mode')).toHaveTextContent('all');
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledWith('all');
    });
    expect(screen.getByTestId('mode')).toHaveTextContent('all');
  });

  it('rolls back to the previous mode when the write fails', async () => {
    fetchMock.mockResolvedValue('blocked');
    putMock.mockRejectedValue(new Error('nope'));
    render(<Probe />);
    await waitFor(() => {
      expect(screen.getByTestId('ready')).toHaveTextContent('ready');
    });

    fireEvent.click(screen.getByText('all'));
    expect(screen.getByTestId('mode')).toHaveTextContent('all');
    await waitFor(() => {
      expect(screen.getByTestId('mode')).toHaveTextContent('blocked');
    });
  });
});
