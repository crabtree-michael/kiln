// Bell notification-settings menu tests (02 §10): the bell opens the panel, the
// current mode reads as selected, picking an option calls back and closes, and
// the permission button reflects the push status.
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { NotificationSettingsMenu } from '@/components/NotificationSettingsMenu';

const OPTION_NAME = {
  default: '^Default',
  blocked: '^Blocked',
  all: '^All updates',
} as const;

function option(value: 'default' | 'all' | 'blocked'): HTMLElement {
  return screen.getByRole('button', { name: new RegExp(OPTION_NAME[value]) });
}

describe('NotificationSettingsMenu', () => {
  it('starts closed and opens on the bell', () => {
    render(<NotificationSettingsMenu mode="blocked" />);
    const panel = document.querySelector('[data-role="notify-settings-panel"]');
    expect(panel).toHaveAttribute('data-open', 'false');
    fireEvent.click(screen.getByRole('button', { name: 'Notification settings' }));
    expect(panel).toHaveAttribute('data-open', 'true');
  });

  it('offers all three modes and marks the current one as selected', () => {
    render(<NotificationSettingsMenu mode="default" onSelectMode={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Notification settings' }));
    expect(option('default')).toHaveAttribute('data-selected', 'true');
    expect(option('blocked')).toHaveAttribute('data-selected', 'false');
    expect(option('all')).toHaveAttribute('data-selected', 'false');
  });

  it('calls onSelectMode with the default mode when chosen', () => {
    const onSelectMode = vi.fn();
    render(<NotificationSettingsMenu mode="all" onSelectMode={onSelectMode} />);
    fireEvent.click(screen.getByRole('button', { name: 'Notification settings' }));
    fireEvent.click(option('default'));
    expect(onSelectMode).toHaveBeenCalledWith('default');
  });

  it('calls onSelectMode and closes when an option is chosen', () => {
    const onSelectMode = vi.fn();
    render(<NotificationSettingsMenu mode="blocked" onSelectMode={onSelectMode} />);
    fireEvent.click(screen.getByRole('button', { name: 'Notification settings' }));
    fireEvent.click(option('all'));
    expect(onSelectMode).toHaveBeenCalledWith('all');
    expect(document.querySelector('[data-role="notify-settings-panel"]')).toHaveAttribute(
      'data-open',
      'false',
    );
  });

  it('disables the options when no handler is wired', () => {
    render(<NotificationSettingsMenu mode="blocked" />);
    fireEvent.click(screen.getByRole('button', { name: 'Notification settings' }));
    expect(option('all')).toBeDisabled();
  });

  it('offers the permission button when push can be enabled', () => {
    const onEnablePush = vi.fn();
    render(
      <NotificationSettingsMenu
        mode="blocked"
        pushStatus="default"
        onEnablePush={onEnablePush}
        onSelectMode={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Notification settings' }));
    const perm = screen.getByRole('button', { name: 'Enable notifications' });
    expect(perm).not.toBeDisabled();
    fireEvent.click(perm);
    expect(onEnablePush).toHaveBeenCalledOnce();
  });

  it('offers to disable and calls onDisablePush when already enabled', () => {
    const onDisablePush = vi.fn();
    render(
      <NotificationSettingsMenu
        mode="blocked"
        pushStatus="enabled"
        onDisablePush={onDisablePush}
        onSelectMode={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Notification settings' }));
    const perm = screen.getByRole('button', { name: 'Disable notifications' });
    expect(perm).not.toBeDisabled();
    fireEvent.click(perm);
    expect(onDisablePush).toHaveBeenCalledOnce();
  });

  it('renders the permission button as a disabled status line when blocked in the browser', () => {
    render(<NotificationSettingsMenu mode="blocked" pushStatus="denied" onEnablePush={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Notification settings' }));
    const perm = screen.getByRole('button', { name: 'Notifications blocked in browser' });
    expect(perm).toBeDisabled();
  });
});
