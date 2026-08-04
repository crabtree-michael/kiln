// The account view's Integrations section (11 §5): one card per API-key
// provider — Amika, Devin — replacing the old flat list of free-text credential
// inputs that `CredentialFields` used to render.
//
// GitHub is deliberately NOT here. Its credential comes from the repo-scoped
// OAuth grant, not a pasted key, and the two surfaces that actually need it ask
// for it in place: the guided setup flow's first step (`Onboarding`) and the
// project form's repo picker (`ConfigFields`). A card here would be a third
// place to connect the same thing, so this section covers only the providers
// you connect by pasting a key.
//
// One card pattern, one row each: the provider's brand mark, its name, whether
// it's connected, and the action. "Connect" (or "Update key" once a key is
// stored) opens a small modal whose input takes the key. Submitting sends just
// that one field, exactly as the old blur/Enter auto-save did, so a successful
// save still chains straight into a verify run (dashboard-store's
// `saveSettings`) and the card's `credential-status` indicator picks up the
// fresh result.
//
// Everything about a provider beyond "is it connected" lives in that modal —
// including Amika's Claude credential ID — so the section itself stays a
// scannable list of one-line rows.
//
// Secrets stay write-only (11 §3 D7): the modal input never carries the stored
// value, only a placeholder built from its status tail, and the draft only
// clears once the save actually succeeds.
import {
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
  type FormEvent,
  type JSX,
  type KeyboardEvent,
} from 'react';
import type { SettingsUpdateRequest, VerifyCheck } from '@/transport/transport';
import type { components } from '@/schema/generated';
import { secretStatusText } from '@/dashboard/secret-status';
import type { CredentialName } from '@/dashboard/dashboard-context';
import {
  API_KEY_PROVIDERS,
  CHECK_NAME_FOR_CREDENTIAL,
  credentialIndicatorStatus,
  updateBodyFor,
  type ApiKeyProvider,
  type CredentialIndicatorStatus,
} from '@/dashboard/integrations-config';

// `MeSettings`/`SecretStatus` aren't among transport.ts's re-exports (only the
// types its own functions traffic in are) — pull them the same way it derives
// its own local aliases, straight off the generated wire schema.
type MeSettings = components['schemas']['MeSettings'];
type SecretStatus = components['schemas']['SecretStatus'];

/** Per-user Anthropic key entry is HIDDEN for now: the deployment supplies the
 * Anthropic key as a global `ANTHROPIC_API_KEY` env setting, and onboarding no
 * longer asks each user for one. The card, its modal, and its commit/verify
 * path are RETAINED (not deleted) behind this env flag so per-user Anthropic
 * keys can be brought back — set `VITE_SHOW_ANTHROPIC_KEY_FIELD=1` — when user
 * management expands, no code change needed. */
const SHOW_ANTHROPIC_KEY_FIELD = import.meta.env.VITE_SHOW_ANTHROPIC_KEY_FIELD === '1';

interface CredentialStatusIndicatorProps {
  name: CredentialName;
  status: CredentialIndicatorStatus;
  message?: string | undefined;
}

/** The card's validity mark: a checkmark once its verify check comes back ok,
 * a cross (with the check's message as a hover title) when it fails, a quiet
 * ellipsis while its save or a verify is in flight, and nothing rendered for
 * `skipped` — `data-status` always carries the real state (tests bind to it),
 * the glyph is just the human-visible layer on top. */
function CredentialStatusIndicator({
  name,
  status,
  message,
}: CredentialStatusIndicatorProps): JSX.Element {
  let glyph: string | null = null;
  if (status === 'ok') {
    glyph = '✓';
  } else if (status === 'failed') {
    glyph = '✗';
  } else if (status === 'pending') {
    glyph = '…';
  }
  return (
    <span
      data-role="credential-status"
      data-name={name}
      data-status={status}
      title={status === 'failed' ? message : undefined}
    >
      {glyph}
    </span>
  );
}

/** Extra, non-secret provider config carried in the key dialog (Amika's Claude
 * credential ID). Described as DATA rather than as markup so the dialog owns
 * the draft: the field resets with the dialog, Save commits it, and Cancel or
 * Escape discards it. A field that rendered here but committed itself on blur
 * would make Cancel save — the one thing Cancel must never do. */
interface ExtraConfig {
  /** `data-role` on the field — the stable selector tests bind to. */
  role: string;
  /** The field's visible label, and so its accessible name. */
  label: string;
  /** The stored value: where the draft starts, and what "unchanged" means. */
  value: string;
  /** The update-body fragment for a changed draft. */
  body: (value: string) => SettingsUpdateRequest;
}

/** What the dialog submits: the pasted key (empty when it was left alone) and
 * the extra field's draft. Either alone is a valid save. */
interface ApiKeyDraft {
  key: string;
  extra: string;
}

/** Whether a draft is a real change worth sending. Blank never is: the field
 * points at stored config, and emptying it was never a way to clear it. The
 * type predicate lets the one caller that needs `extra.body` narrow to it. */
function extraChanged(extra: ExtraConfig | undefined, draft: string): extra is ExtraConfig {
  return extra !== undefined && draft.trim() !== '' && draft.trim() !== extra.value;
}

interface ApiKeyModalProps {
  spec: ApiKeyProvider;
  /** The stored value's presence+tail, so the input can advertise
   * "configured · …<tail>" without ever holding the value itself. */
  status: SecretStatus;
  saving: boolean;
  /** Extra, non-secret provider config rendered below the key input. Saved by
   * this dialog's Save, in the SAME request as the key — so changing it never
   * requires re-pasting a key you already stored, and neither half can commit
   * behind the other's back. */
  extra?: ExtraConfig | undefined;
  onCancel: () => void;
  /** Resolves `true` when the change was actually stored — the modal only
   * closes then, so a failed save leaves the typed values in place rather than
   * silently discarding them. */
  onSubmit: (draft: ApiKeyDraft) => Promise<boolean>;
}

/** The "paste your key" dialog. Deliberately small: one labelled input, any
 * extra config for that provider, Save, Cancel. Escape and the backdrop both
 * cancel. */
function ApiKeyModal({
  spec,
  status,
  saving,
  extra,
  onCancel,
  onSubmit,
}: ApiKeyModalProps): JSX.Element {
  const [value, setValue] = useState('');
  // Seeded from the stored value, and owned by the dialog: closing unmounts it,
  // so an abandoned edit leaves nothing behind to reappear on reopen.
  const [extraDraft, setExtraDraft] = useState(extra?.value ?? '');
  const inputRef = useRef<HTMLInputElement>(null);
  // In-flight guard, synchronous on purpose: Enter submits and an impatient
  // second Enter (or a click on Save) can land in the same task, long before
  // the store's `pendingCredentials` re-renders this as `saving` — a ref is
  // the only thing that reliably makes that pair a single save.
  const inFlight = useRef(false);
  const heading = status.set ? `Update ${spec.title} key` : `Connect ${spec.title}`;
  // Either half alone is a save: with a key already stored, changing just the
  // credential ID must not require re-pasting the key to enable Save.
  const canSubmit = value.trim() !== '' || extraChanged(extra, extraDraft);

  // Land focus in the input on open: the dialog exists primarily to take this
  // one value, so the user should be able to paste immediately.
  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    if (!canSubmit || saving || inFlight.current) {
      return;
    }
    inFlight.current = true;
    void onSubmit({ key: value.trim(), extra: extraDraft })
      .then((succeeded) => {
        if (succeeded) {
          // Only on success: a failed save leaves the dialog open with the
          // typed values in place rather than silently discarding them.
          onCancel();
        }
      })
      .finally(() => {
        inFlight.current = false;
      });
  };

  return (
    <div
      data-role="api-key-backdrop"
      onClick={onCancel}
      onKeyDown={(event: KeyboardEvent<HTMLDivElement>) => {
        if (event.key === 'Escape') {
          onCancel();
        }
      }}
    >
      {/* The dialog swallows the backdrop's dismiss-on-click; clicks inside it
          are ordinary interactions, not a request to close. */}
      <form
        data-role="api-key-modal"
        role="dialog"
        aria-modal="true"
        aria-label={heading}
        onClick={(event) => {
          event.stopPropagation();
        }}
        onSubmit={handleSubmit}
      >
        <h3 data-role="api-key-title">{heading}</h3>
        <label>
          {spec.label}
          <input
            ref={inputRef}
            type="password"
            data-role="api-key-input"
            value={value}
            placeholder={status.set ? secretStatusText(status) : ''}
            disabled={saving}
            onChange={(event: ChangeEvent<HTMLInputElement>) => {
              setValue(event.target.value);
            }}
          />
        </label>
        {extra !== undefined && (
          // Not a secret, so unlike the key it opens holding its stored value —
          // there is nothing to hide, and seeing the current one is the point.
          <label data-role={extra.role}>
            {extra.label}
            <input
              type="text"
              value={extraDraft}
              disabled={saving}
              onChange={(event: ChangeEvent<HTMLInputElement>) => {
                setExtraDraft(event.target.value);
              }}
            />
          </label>
        )}
        <div data-role="api-key-actions">
          <button type="button" data-role="api-key-cancel" onClick={onCancel}>
            Cancel
          </button>
          <button type="submit" data-role="api-key-save" disabled={saving || !canSubmit}>
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </form>
    </div>
  );
}

interface ApiKeyCardProps {
  spec: ApiKeyProvider;
  status: SecretStatus;
  pending: boolean;
  indicatorStatus: CredentialIndicatorStatus;
  message?: string | undefined;
  /** Passed straight through to the dialog — extra config belongs with the key,
   * not on the row. */
  extra?: ExtraConfig | undefined;
  onSave: (body: SettingsUpdateRequest) => Promise<boolean>;
}

/** One provider as a single row: brand mark, name, connection state, and the
 * action pinned to the right edge. No detail line under it — the state dot and
 * the Connect/Update-key wording already say everything the row needs to, and
 * the stored key's fingerprint shows in the dialog where you'd act on it. */
function ApiKeyCard({
  spec,
  status,
  pending,
  indicatorStatus,
  message,
  extra,
  onSave,
}: ApiKeyCardProps): JSX.Element {
  const [open, setOpen] = useState(false);
  const connectedLabel = status.set ? 'Connected' : 'Not connected';

  // One request for whatever the dialog actually changed — a key, the extra
  // config, or both — so a single save chains a single verify run.
  const submit = (draft: ApiKeyDraft): Promise<boolean> =>
    onSave({
      ...(draft.key === '' ? {} : updateBodyFor(spec.credential, draft.key)),
      ...(extraChanged(extra, draft.extra) ? extra.body(draft.extra.trim()) : {}),
    });

  return (
    <div
      data-role="integration-card"
      data-provider={spec.provider}
      data-connected={String(status.set)}
    >
      <div data-role="integration-header">
        {/* Decorative: the provider's name sits right beside it, so the mark
            adds no information a screen reader would miss. */}
        <span data-role="integration-mark" aria-hidden="true">
          {spec.logo === undefined ? spec.title.slice(0, 1) : <img src={spec.logo} alt="" />}
        </span>
        <span data-role="integration-title">{spec.title}</span>
        {/* Connection state as a dot, not a label: on a phone a "Not connected"
            pill was the widest thing in the row and read louder than the
            provider's own name. Filled green vs. a grey ring carries it on
            screen (see Dashboard.css); the accessible name and the hover title
            still say it in words. */}
        <span
          data-role="integration-connected"
          data-connected={String(status.set)}
          role="img"
          aria-label={connectedLabel}
          title={connectedLabel}
        />
        <div data-role="integration-action">
          <CredentialStatusIndicator
            name={spec.credential}
            status={indicatorStatus}
            message={message}
          />
          <button
            type="button"
            data-role="integration-connect"
            data-provider={spec.provider}
            disabled={pending}
            onClick={() => {
              setOpen(true);
            }}
          >
            {status.set ? 'Update key' : 'Connect'}
          </button>
        </div>
      </div>
      {open && (
        <ApiKeyModal
          spec={spec}
          status={status}
          saving={pending}
          extra={extra}
          onCancel={() => {
            setOpen(false);
          }}
          onSubmit={submit}
        />
      )}
    </div>
  );
}

/** Amika needs one more, non-secret, piece of config: which stored Claude
 * credential its sandboxes run under. It rides inside the Amika key dialog
 * rather than on the row — the row is a one-line summary, and this is something
 * you set while you are already thinking about that provider's credentials. */
const AMIKA_CRED_ID: Omit<ExtraConfig, 'value'> = {
  role: 'amika-cred-id',
  label: 'Amika Claude credential ID',
  body: (value: string) => ({ amika_claude_cred_id: value }),
};

export interface IntegrationsProps {
  settings: MeSettings;
  /** The credentials whose save/verify is currently in flight — threaded
   * straight through from the store; drives each card's indicator and its
   * connect button independently. */
  pendingCredentials: ReadonlySet<CredentialName>;
  /** `true` while a verify run is in flight — applies to every indicator at
   * once (one run checks every provider). */
  verifying: boolean;
  verifyChecks: VerifyCheck[] | null;
  onSave: (body: SettingsUpdateRequest) => Promise<boolean>;
}

export function Integrations({
  settings,
  pendingCredentials,
  verifying,
  verifyChecks,
  onSave,
}: IntegrationsProps): JSX.Element {
  const checkFor = (name: CredentialName): VerifyCheck | undefined =>
    verifyChecks?.find((candidate) => candidate.name === CHECK_NAME_FOR_CREDENTIAL[name]);

  const cards = API_KEY_PROVIDERS.filter(
    (spec) => spec.credential !== 'anthropic_api_key' || SHOW_ANTHROPIC_KEY_FIELD,
  );

  return (
    <section data-role="integrations">
      {cards.map((spec) => (
        <ApiKeyCard
          key={spec.credential}
          spec={spec}
          status={settings[spec.credential]}
          pending={pendingCredentials.has(spec.credential)}
          indicatorStatus={credentialIndicatorStatus(
            spec.credential,
            pendingCredentials,
            verifying,
            checkFor(spec.credential),
          )}
          message={checkFor(spec.credential)?.message}
          extra={
            spec.credential === 'amika_api_key'
              ? { ...AMIKA_CRED_ID, value: settings.amika_claude_cred_id }
              : undefined
          }
          onSave={onSave}
        />
      ))}
    </section>
  );
}
