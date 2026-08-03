// The account view's Integrations section (11 §5): one card per provider —
// GitHub, Amika, Devin — replacing the old flat list of free-text credential
// inputs that `CredentialFields` used to render.
//
// Two connect shapes, one card pattern:
//   * API-key providers (Amika, Devin — and Anthropic when its retained field
//     is re-enabled) — "Connect" opens a small modal whose single input takes
//     the key. Submitting sends just that one field, exactly as the old
//     blur/Enter auto-save did, so a successful save still chains straight
//     into a verify run (dashboard-store's `saveSettings`) and the card's
//     `credential-status` indicator picks up the fresh result.
//   * GitHub — "Connect" is the REPO-SCOPED OAuth grant (`/auth/github/connect`,
//     a backend route, so a plain full-page anchor and NOT a router Link).
//     Signing in grants no scopes, so it is this grant — and only this grant —
//     that yields a credential able to clone and push; the card says so out
//     loud, since GitHub's consent screen will ask for `repo`. Once connected
//     the card shows the linked account and offers "Switch account", which
//     re-runs the same flow; that action sits in the card's right-hand action
//     slot, never inline with the identity text.
//
// A token stored by the removed manual field lands in the same slot this flow
// writes, so it carries forward: the card reports it as connected rather than
// stranding it (see GitHubCard's status handling).
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
  type ReactNode,
} from 'react';
import type { SettingsUpdateRequest, VerifyCheck } from '@/transport/transport';
import type { components } from '@/schema/generated';
import { secretStatusText } from '@/dashboard/secret-status';
import type { CredentialName } from '@/dashboard/dashboard-context';
import { GITHUB_CONNECT_PATH, type GitHubRepos } from '@/dashboard/use-github-repos';

// `MeSettings`/`SecretStatus` aren't among transport.ts's re-exports (only the
// types its own functions traffic in are) — pull them the same way it derives
// its own local aliases, straight off the generated wire schema.
type MeSettings = components['schemas']['MeSettings'];
type SecretStatus = components['schemas']['SecretStatus'];
type GitHubConnection = components['schemas']['GitHubConnection'];

/** The credentials a user pastes as a key. GitHub is deliberately absent: it is
 * connected through OAuth, not by pasting a token. */
type ApiKeyCredential = Exclude<CredentialName, 'github_auth_token'>;

/** Which `VerifyCheck.name` each card's indicator reads from — GitHub's
 * connection guards repo access, so it maps to the "repo" check rather than a
 * standalone "github" one. */
const CHECK_NAME_FOR_CREDENTIAL: Record<CredentialName, VerifyCheck['name']> = {
  anthropic_api_key: 'anthropic',
  amika_api_key: 'amika',
  devin_api_key: 'devin',
  github_auth_token: 'repo',
};

// Every affordance on the GitHub card (Connect, Reconnect, Switch account)
// points at the shared `GITHUB_CONNECT_PATH` — the repo-scoped grant — imported
// rather than restated here, so this card and the settings repo picker can't
// drift onto different routes.

/** What connecting actually authorizes, stated on the card itself so the
 * `repo` grant on GitHub's consent screen is expected rather than alarming. */
const GITHUB_ACCESS_NOTE =
  'Connecting grants Kiln read and write access to your repositories — it needs ' +
  'that to clone your repo, read it, and push the branches your agents produce.';

/** Per-user Anthropic key entry is HIDDEN for now: the deployment supplies the
 * Anthropic key as a global `ANTHROPIC_API_KEY` env setting, and onboarding no
 * longer asks each user for one. The card, its modal, and its commit/verify
 * path are RETAINED (not deleted) behind this env flag so per-user Anthropic
 * keys can be brought back — set `VITE_SHOW_ANTHROPIC_KEY_FIELD=1` — when user
 * management expands, no code change needed. */
const SHOW_ANTHROPIC_KEY_FIELD = import.meta.env.VITE_SHOW_ANTHROPIC_KEY_FIELD === '1';

interface ApiKeyProvider {
  /** The settings field this card writes, and the key its indicator reads. */
  credential: ApiKeyCredential;
  /** `data-provider` on the card — the stable selector tests/e2e bind to. */
  provider: string;
  /** Card heading. */
  title: string;
  /** The modal input's label. Kept identical to the old field label so the
   * accessible name (`getByLabelText('Amika API key')`) is unchanged. */
  label: string;
}

/** The API-key cards, in render order. Anthropic leads only when its retained
 * field is switched back on. */
const API_KEY_PROVIDERS: ApiKeyProvider[] = [
  {
    credential: 'anthropic_api_key',
    provider: 'anthropic',
    title: 'Anthropic',
    label: 'Anthropic API key',
  },
  { credential: 'amika_api_key', provider: 'amika', title: 'Amika', label: 'Amika API key' },
  { credential: 'devin_api_key', provider: 'devin', title: 'Devin', label: 'Devin API key' },
];

/** The one-field update body for a key card. A `switch` rather than a computed
 * key so the result is a real `SettingsUpdateRequest`, not an index signature. */
function updateBodyFor(credential: ApiKeyCredential, value: string): SettingsUpdateRequest {
  switch (credential) {
    case 'anthropic_api_key':
      return { anthropic_api_key: value };
    case 'amika_api_key':
      return { amika_api_key: value };
    case 'devin_api_key':
      return { devin_api_key: value };
  }
}

type CredentialIndicatorStatus = 'ok' | 'failed' | 'skipped' | 'pending';

/** `pending` covers two windows: this card's own save request (it stays in
 * `pendingCredentials` for the whole save + chained-verify span, independent
 * of any other card's in-flight save), and any verify run at all (one verify
 * call checks every provider at once, so all the indicators go pending
 * together while it's in flight) — either means "the last known result can't
 * be trusted yet". Absent any check result, the card reads `skipped` (nothing
 * has verified it — same as an explicit "skipped" check, and rendered the same
 * way: no glyph). */
function credentialIndicatorStatus(
  name: CredentialName,
  pendingCredentials: ReadonlySet<CredentialName>,
  verifying: boolean,
  check: VerifyCheck | undefined,
): CredentialIndicatorStatus {
  if (pendingCredentials.has(name) || verifying) {
    return 'pending';
  }
  return check?.status ?? 'skipped';
}

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

interface IntegrationCardProps {
  provider: string;
  title: string;
  connected: boolean;
  /** Overrides the chip's text when a card has a state finer than the
   * connected/not-connected pair (GitHub's "Reconnect needed"). */
  statusLabel?: string | undefined;
  /** Marks the chip as an alarm state — something stored is broken and needs
   * the user. Distinct from merely unconfigured, which stays neutral. */
  attention?: boolean;
  /** The card's right-hand action slot — Connect / Update key / Switch
   * account. Always laid out at the card's right edge, never inline with the
   * detail text. */
  action: ReactNode;
  /** Anything below the header row: the stored-secret status line, the linked
   * GitHub account, extra provider config. */
  children?: ReactNode;
}

function IntegrationCard({
  provider,
  title,
  connected,
  statusLabel,
  attention = false,
  action,
  children,
}: IntegrationCardProps): JSX.Element {
  return (
    <div data-role="integration-card" data-provider={provider} data-connected={String(connected)}>
      <div data-role="integration-header">
        <span data-role="integration-title">{title}</span>
        <span
          data-role="integration-connected"
          data-connected={String(connected)}
          data-attention={attention ? 'true' : undefined}
        >
          {statusLabel ?? (connected ? 'Connected' : 'Not connected')}
        </span>
        <div data-role="integration-action">{action}</div>
      </div>
      {children}
    </div>
  );
}

interface ApiKeyModalProps {
  title: string;
  label: string;
  /** The stored value's presence+tail, so the input can advertise
   * "configured · …<tail>" without ever holding the value itself. */
  status: SecretStatus;
  saving: boolean;
  onCancel: () => void;
  /** Resolves `true` when the key was actually stored — the modal only closes
   * then, so a failed save leaves the typed value in place rather than
   * silently discarding it. */
  onSubmit: (value: string) => Promise<boolean>;
}

/** The "paste your key" dialog. Deliberately small: one labelled input, Save,
 * Cancel. Escape and the backdrop both cancel. */
function ApiKeyModal({
  title,
  label,
  status,
  saving,
  onCancel,
  onSubmit,
}: ApiKeyModalProps): JSX.Element {
  const [value, setValue] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);
  // In-flight guard, synchronous on purpose: Enter submits and an impatient
  // second Enter (or a click on Save) can land in the same task, long before
  // the store's `pendingCredentials` re-renders this as `saving` — a ref is
  // the only thing that reliably makes that pair a single save.
  const inFlight = useRef(false);

  // Land focus in the input on open: the dialog exists solely to take this one
  // value, so the user should be able to paste immediately.
  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const trimmed = value.trim();
    if (trimmed === '' || saving || inFlight.current) {
      return;
    }
    inFlight.current = true;
    void onSubmit(trimmed)
      .then((succeeded) => {
        if (succeeded) {
          // Only on success: a failed save leaves the dialog open with the
          // typed value in place rather than silently discarding it.
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
        aria-label={`Connect ${title}`}
        onClick={(event) => {
          event.stopPropagation();
        }}
        onSubmit={handleSubmit}
      >
        <h3 data-role="api-key-title">Connect {title}</h3>
        <label>
          {label}
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
        <div data-role="api-key-actions">
          <button type="button" data-role="api-key-cancel" onClick={onCancel}>
            Cancel
          </button>
          <button type="submit" data-role="api-key-save" disabled={saving || value.trim() === ''}>
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
  onSave: (body: SettingsUpdateRequest) => Promise<boolean>;
  children?: ReactNode;
}

function ApiKeyCard({
  spec,
  status,
  pending,
  indicatorStatus,
  message,
  onSave,
  children,
}: ApiKeyCardProps): JSX.Element {
  const [open, setOpen] = useState(false);

  const submit = (value: string): Promise<boolean> => onSave(updateBodyFor(spec.credential, value));

  return (
    <IntegrationCard
      provider={spec.provider}
      title={spec.title}
      connected={status.set}
      action={
        <>
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
        </>
      }
    >
      <span data-role="secret-status" data-name={spec.credential} data-set={String(status.set)}>
        {secretStatusText(status)}
      </span>
      {children}
      {open && (
        <ApiKeyModal
          title={spec.title}
          label={spec.label}
          status={status}
          saving={pending}
          onCancel={() => {
            setOpen(false);
          }}
          onSubmit={submit}
        />
      )}
    </IntegrationCard>
  );
}

interface GitHubCardProps {
  /** The repo-credential state, straight off `GET /api/me`. */
  connection: GitHubConnection;
  /** The connected account's repos — the project form's picker reads the same
   * credential, so the card reports what that grant actually bought. */
  github: GitHubRepos;
  /** The account the SESSION signed in with — the fallback label for a
   * carried-over credential, whose own account was never recorded. */
  sessionLogin: string;
  indicatorStatus: CredentialIndicatorStatus;
  message?: string | undefined;
}

/** GitHub connects through the repo-scoped OAuth grant rather than a pasted
 * token. The card renders the four states of that credential:
 *
 *   disconnected    → "Connect"
 *   connected       → the linked account + "Switch account"
 *   unknown         → treated as connected: a token carried over from the
 *                     removed manual field, presumed good until a verify run
 *                     says otherwise. It is never downgraded by the refactor.
 *   needs_reconnect → GitHub reported scopes without `repo`, so the credential
 *                     cannot clone or push: say so and offer "Reconnect".
 *
 * Whatever the state, the action lives in the card's right-hand slot, never
 * inline with the account text. */
function GitHubCard({
  connection,
  github,
  sessionLogin,
  indicatorStatus,
  message,
}: GitHubCardProps): JSX.Element {
  const status = connection.status;
  const needsReconnect = status === 'needs_reconnect';
  // A stored credential counts as connected unless GitHub positively told us
  // its scopes are insufficient — that is the carry-forward guarantee.
  const connected = status === 'connected' || status === 'unknown';
  // The credential's own account when it was recorded (a real connect), else
  // the session's — a carried-over token has no recorded owner.
  const login = connection.login || sessionLogin;

  let action = 'Connect';
  if (needsReconnect) {
    action = 'Reconnect';
  } else if (connected) {
    action = 'Switch account';
  }

  return (
    <IntegrationCard
      provider="github"
      title="GitHub"
      connected={connected}
      statusLabel={needsReconnect ? 'Reconnect needed' : undefined}
      attention={needsReconnect}
      action={
        <>
          <CredentialStatusIndicator
            name="github_auth_token"
            status={indicatorStatus}
            message={message}
          />
          {/* A plain full-page anchor, NOT a router Link: the connect route is
              backend-owned, so the browser must actually leave the app. */}
          <a
            href={GITHUB_CONNECT_PATH}
            data-role={
              connected && !needsReconnect ? 'github-switch-account' : 'integration-connect'
            }
            data-provider="github"
            data-status={status}
          >
            {action}
          </a>
        </>
      }
    >
      {connected && login !== '' && <span data-role="github-account">@{login}</span>}
      {/* What the grant actually bought, from the same credential the project
          form's repo picker reads — so the two can never disagree. */}
      {connected && !github.loading && github.connected && (
        <span data-role="github-repo-count">
          {github.repos.length} {github.repos.length === 1 ? 'repository' : 'repositories'}{' '}
          available to link to a project.
        </span>
      )}
      {needsReconnect && (
        <p data-role="github-reconnect-note">
          The connected GitHub credential doesn’t include repository access, so agents can’t clone
          or push. Reconnect to grant it — nothing else changes.
        </p>
      )}
      {/* Stated up front, on the card, so the `repo` permission GitHub asks for
          on the consent screen is expected rather than alarming. */}
      <p data-role="github-access-note">{GITHUB_ACCESS_NOTE}</p>
    </IntegrationCard>
  );
}

export interface IntegrationsProps {
  settings: MeSettings;
  /** The connected account's repos, for the GitHub card's summary line. */
  github: GitHubRepos;
  /** The GitHub account the session signed in with (`me.user.github_login`). */
  githubLogin: string;
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
  github,
  githubLogin,
  pendingCredentials,
  verifying,
  verifyChecks,
  onSave,
}: IntegrationsProps): JSX.Element {
  const [amikaClaudeCredId, setAmikaClaudeCredId] = useState(settings.amika_claude_cred_id);
  // In-flight guard for the credential-ID field, synchronous on purpose: Enter
  // fires a commit and the resulting focus loss (or an explicit Tab) fires blur
  // in the same task, long before the store's pending state re-renders — a ref
  // is the only thing that reliably makes that pair a single save.
  const credIdInFlight = useRef(false);

  const checkFor = (name: CredentialName): VerifyCheck | undefined =>
    verifyChecks?.find((candidate) => candidate.name === CHECK_NAME_FOR_CREDENTIAL[name]);

  const indicatorFor = (name: CredentialName): CredentialIndicatorStatus =>
    credentialIndicatorStatus(name, pendingCredentials, verifying, checkFor(name));

  // Not a secret — the field just shows the live value, so there's nothing to
  // clear on success; only save when it actually changed.
  const commitCredId = (): void => {
    const trimmed = amikaClaudeCredId.trim();
    if (trimmed === '' || trimmed === settings.amika_claude_cred_id || credIdInFlight.current) {
      return;
    }
    credIdInFlight.current = true;
    void onSave({ amika_claude_cred_id: trimmed }).finally(() => {
      credIdInFlight.current = false;
    });
  };

  const cards = API_KEY_PROVIDERS.filter(
    (spec) => spec.credential !== 'anthropic_api_key' || SHOW_ANTHROPIC_KEY_FIELD,
  );

  return (
    <section data-role="integrations">
      <GitHubCard
        connection={settings.github_connection}
        github={github}
        sessionLogin={githubLogin}
        indicatorStatus={indicatorFor('github_auth_token')}
        message={checkFor('github_auth_token')?.message}
      />

      {cards.map((spec) => (
        <ApiKeyCard
          key={spec.credential}
          spec={spec}
          status={settings[spec.credential]}
          pending={pendingCredentials.has(spec.credential)}
          indicatorStatus={indicatorFor(spec.credential)}
          message={checkFor(spec.credential)?.message}
          onSave={onSave}
        >
          {/* Amika needs one more, non-secret, piece of config: which stored
              Claude credential its sandboxes run under. It rides on the Amika
              card rather than floating loose in the account view. */}
          {spec.credential === 'amika_api_key' && (
            <label data-role="amika-cred-id">
              Amika Claude credential ID
              <input
                type="text"
                value={amikaClaudeCredId}
                onChange={(event: ChangeEvent<HTMLInputElement>) => {
                  setAmikaClaudeCredId(event.target.value);
                }}
                onBlur={commitCredId}
                onKeyDown={(event: KeyboardEvent<HTMLInputElement>) => {
                  if (event.key === 'Enter') {
                    event.preventDefault();
                    commitCredId();
                  }
                }}
              />
            </label>
          )}
        </ApiKeyCard>
      ))}
    </section>
  );
}
