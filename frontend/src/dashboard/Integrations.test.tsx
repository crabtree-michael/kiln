// Integrations section (11 §5): a connect card per API-key provider. These
// cover the card-level contract in isolation — the row each provider renders,
// and the key modal's open/save/dismiss behaviour. The wired-up flow (save →
// chained verify → indicator) lives in Dashboard.test.tsx against the real
// store.
//
// GitHub is not part of this section: it connects through the repo-scoped OAuth
// grant, asked for in the setup flow and the project form instead (see
// Onboarding.test.tsx / Dashboard.test.tsx).
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { Integrations } from '@/dashboard/Integrations';
import type { components } from '@/schema/generated';
import type { VerifyCheck } from '@/transport/transport';

type MeSettings = components['schemas']['MeSettings'];

function settings(overrides: Partial<MeSettings> = {}): MeSettings {
  return {
    anthropic_api_key: { set: false, tail: '' },
    amika_api_key: { set: false, tail: '' },
    devin_api_key: { set: false, tail: '' },
    github_auth_token: { set: false, tail: '' },
    github_connection: { status: 'disconnected', login: '', installation_id: 0, configure_url: '' },
    amika_claude_cred_id: '',
    ...overrides,
  };
}

interface RenderOptions {
  settingsOverrides?: Partial<MeSettings>;
  verifyChecks?: VerifyCheck[] | null;
  onSave?: (body: unknown) => Promise<boolean>;
}

function renderIntegrations(options: RenderOptions = {}): {
  onSave: ReturnType<typeof vi.fn>;
} {
  const onSave = vi.fn(options.onSave ?? (() => Promise.resolve(true)));
  render(
    <Integrations
      settings={settings(options.settingsOverrides)}
      pendingCredentials={new Set()}
      verifying={false}
      verifyChecks={options.verifyChecks ?? null}
      onSave={onSave}
    />,
  );
  return { onSave };
}

function card(provider: string): HTMLElement {
  const el = document.querySelector<HTMLElement>(
    `[data-role="integration-card"][data-provider="${provider}"]`,
  );
  if (el === null) {
    throw new Error(`expected an integration card for ${provider}`);
  }
  return el;
}

/** Opens a provider's key dialog and returns its key input. */
function openModal(provider: string, label: string): HTMLElement {
  fireEvent.click(within(card(provider)).getByRole('button', { name: /Connect|Update key/ }));
  return screen.getByLabelText(label);
}

describe('Integrations', () => {
  it('renders one card per API-key provider — and none for GitHub', () => {
    renderIntegrations();
    // Anthropic stays hidden behind its retained env flag (off by default), so
    // Amika and Devin are the whole section.
    expect(document.querySelectorAll('[data-role="integration-card"]')).toHaveLength(2);
    expect(card('amika')).toBeInTheDocument();
    expect(card('devin')).toBeInTheDocument();

    // GitHub is connected through OAuth in the setup flow and the project form,
    // not from here — neither a card nor a free-text token field.
    expect(
      document.querySelector('[data-role="integration-card"][data-provider="github"]'),
    ).toBeNull();
    expect(screen.queryByLabelText('GitHub token')).toBeNull();
    expect(document.querySelector('a[href="/auth/github/connect"]')).toBeNull();
  });

  it('each row carries the provider’s brand mark and nothing under it', () => {
    renderIntegrations();
    const mark = (provider: string): Element | null =>
      card(provider).querySelector('[data-role="integration-mark"] img');
    // The same assets the landing page's agent orbit uses.
    expect(mark('amika')).toHaveAttribute('src', '/logos/amika.svg');
    expect(mark('devin')).toHaveAttribute('src', '/logos/devin.svg');
    // Decorative — the provider's name is right beside it.
    expect(mark('amika')).toHaveAttribute('alt', '');

    // The old stored-secret detail line is gone: the row is the header alone.
    expect(document.querySelector('[data-role="secret-status"]')).toBeNull();
    expect(card('amika').children).toHaveLength(1);
  });

  it('an unconfigured key card says Connect; a configured one says Update key', () => {
    renderIntegrations({ settingsOverrides: { devin_api_key: { set: true, tail: 'wxyz' } } });
    // The state shows as a dot, not a label — so it's the dot's accessible name
    // that has to keep saying it in words.
    const dot = (provider: string): HTMLElement =>
      within(card(provider)).getByRole('img', { name: /connected/i });

    expect(card('amika')).toHaveAttribute('data-connected', 'false');
    expect(dot('amika')).toHaveAccessibleName('Not connected');
    expect(dot('amika')).toHaveAttribute('data-connected', 'false');
    expect(within(card('amika')).getByRole('button', { name: 'Connect' })).toBeInTheDocument();

    expect(card('devin')).toHaveAttribute('data-connected', 'true');
    expect(dot('devin')).toHaveAccessibleName('Connected');
    expect(dot('devin')).toHaveAttribute('data-connected', 'true');
    expect(within(card('devin')).getByRole('button', { name: 'Update key' })).toBeInTheDocument();

    // The old text label is gone from the row entirely — it was the thing that
    // read oversized on mobile.
    expect(within(card('devin')).queryByText('Connected')).toBeNull();
    expect(within(card('amika')).queryByText('Not connected')).toBeNull();
  });

  it('the stored key’s fingerprint shows in the dialog, never the value', () => {
    renderIntegrations({ settingsOverrides: { devin_api_key: { set: true, tail: 'wxyz' } } });
    const input = openModal('devin', 'Devin API key');
    // Write-only: the input opens empty and only advertises the tail.
    expect(input).toHaveValue('');
    expect(input).toHaveAttribute('placeholder', 'configured · …wxyz');
    expect(screen.getByRole('dialog')).toHaveAccessibleName('Update Devin key');
  });

  it('Connect opens a modal that sends only that provider’s key', async () => {
    const { onSave } = renderIntegrations();
    const input = openModal('devin', 'Devin API key');
    fireEvent.change(input, { target: { value: '  cog-abc  ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith({ devin_api_key: 'cog-abc' });
    });
    expect(onSave).toHaveBeenCalledTimes(1);
  });

  it('Escape and the backdrop both dismiss the modal without saving', () => {
    const { onSave } = renderIntegrations();
    const modal = (): Element | null => document.querySelector('[data-role="api-key-modal"]');
    const backdrop = (): Element => {
      const el = document.querySelector('[data-role="api-key-backdrop"]');
      if (el === null) {
        throw new Error('expected the modal backdrop');
      }
      return el;
    };

    fireEvent.keyDown(openModal('amika', 'Amika API key'), { key: 'Escape' });
    expect(modal()).toBeNull();

    openModal('amika', 'Amika API key');
    fireEvent.click(backdrop());
    expect(modal()).toBeNull();

    // A click inside the dialog must NOT bubble up into the backdrop dismiss.
    fireEvent.click(openModal('amika', 'Amika API key'));
    expect(modal()).not.toBeNull();

    expect(onSave).not.toHaveBeenCalled();
  });

  it('a failed save keeps the modal open with the typed value intact', async () => {
    renderIntegrations({ onSave: () => Promise.resolve(false) });
    const input = openModal('amika', 'Amika API key');
    fireEvent.change(input, { target: { value: 'sk-bad' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(document.querySelector('[data-role="api-key-modal"]')).not.toBeNull();
    });
    expect(screen.getByLabelText('Amika API key')).toHaveValue('sk-bad');
  });

  it('the Amika dialog carries the non-secret Claude credential ID', () => {
    renderIntegrations({ settingsOverrides: { amika_claude_cred_id: 'cred-1' } });
    // It lives in the dialog, not loose on the row.
    expect(screen.queryByLabelText('Amika Claude credential ID')).toBeNull();

    openModal('amika', 'Amika API key');
    const input = screen.getByLabelText('Amika Claude credential ID');
    expect(document.querySelector('[data-role="api-key-modal"]')?.contains(input)).toBe(true);
    // Not a secret, so unlike the key it opens holding its stored value.
    expect(input).toHaveValue('cred-1');
  });

  it('Save commits the credential ID on its own — no key paste required', async () => {
    const { onSave } = renderIntegrations({
      settingsOverrides: { amika_api_key: { set: true, tail: 'wxyz' } },
    });
    openModal('amika', 'Amika API key');
    const save = screen.getByRole('button', { name: 'Save' });
    // Nothing has changed yet, so there is nothing to save.
    expect(save).toBeDisabled();

    fireEvent.change(screen.getByLabelText('Amika Claude credential ID'), {
      target: { value: ' cred-9 ' },
    });
    // A changed ID is a save in its own right: re-pasting a key that is already
    // stored must never be the price of editing this field.
    expect(save).toBeEnabled();
    fireEvent.click(save);

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith({ amika_claude_cred_id: 'cred-9' });
    });
    expect(onSave).toHaveBeenCalledTimes(1);
  });

  it('Save sends a key and the credential ID together, in one request', async () => {
    const { onSave } = renderIntegrations();
    fireEvent.change(openModal('amika', 'Amika API key'), { target: { value: 'sk-amika' } });
    fireEvent.change(screen.getByLabelText('Amika Claude credential ID'), {
      target: { value: 'cred-9' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    // One request, so one chained verify run — not two saves racing each other.
    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith({
        amika_api_key: 'sk-amika',
        amika_claude_cred_id: 'cred-9',
      });
    });
    expect(onSave).toHaveBeenCalledTimes(1);
  });

  // The regression this shape exists to prevent: the field used to commit itself
  // on blur, so dismissing the dialog — which blurs it — saved the very edit the
  // user was backing out of.
  it('Cancel and Escape discard the credential ID instead of saving it', () => {
    const { onSave } = renderIntegrations({
      settingsOverrides: { amika_claude_cred_id: 'cred-1' },
    });
    const edit = (value: string): void => {
      fireEvent.change(screen.getByLabelText('Amika Claude credential ID'), { target: { value } });
    };

    openModal('amika', 'Amika API key');
    edit('cred-9');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onSave).not.toHaveBeenCalled();

    openModal('amika', 'Amika API key');
    // The discarded draft is gone, not lying in wait for the next open.
    expect(screen.getByLabelText('Amika Claude credential ID')).toHaveValue('cred-1');
    edit('cred-9');
    fireEvent.keyDown(screen.getByLabelText('Amika API key'), { key: 'Escape' });
    expect(document.querySelector('[data-role="api-key-modal"]')).toBeNull();
    expect(onSave).not.toHaveBeenCalled();
  });

  it('maps each card’s indicator to its own verify check', () => {
    renderIntegrations({
      verifyChecks: [
        { name: 'amika', status: 'ok', message: 'reachable' },
        { name: 'devin', status: 'failed', message: 'invalid key' },
        { name: 'repo', status: 'ok', message: 'repo reachable' },
      ],
    });
    const indicator = (name: string): Element | null =>
      document.querySelector(`[data-role="credential-status"][data-name="${name}"]`);

    expect(indicator('amika_api_key')).toHaveAttribute('data-status', 'ok');
    expect(indicator('devin_api_key')).toHaveAttribute('data-status', 'failed');
    expect(indicator('devin_api_key')).toHaveAttribute('title', 'invalid key');
    // The "repo" check has no card here — GitHub is not part of this section.
    expect(indicator('github_auth_token')).toBeNull();
  });
});
