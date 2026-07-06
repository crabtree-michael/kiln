// Controlled forms for the dashboard's two config surfaces (11 §5): project
// (name/repo/snapshot/model/workers) and credentials (secrets + Amika config).
// Both are seeded from the current `Me`. Project still submits explicitly via
// its "Save project" button; credentials auto-save per field instead (
// dashboard UX update): each secret input commits on blur AND Enter, only
// when its draft is non-empty, sending just that one field — secrets are
// write-only (11 §3 D7): the input never carries the stored value, only a
// placeholder built from its status tail, and the draft only clears once the
// save actually succeeds. A successful credential save chains straight into a
// verify run (dashboard-store's `saveSettings`), so the right-of-input
// `credential-status` indicator picks up the fresh check result with no
// separate "test connections" step.
import { useState, type ChangeEvent, type FormEvent, type JSX, type KeyboardEvent } from 'react';
import type {
  MeProject,
  ProjectUpdateRequest,
  SettingsUpdateRequest,
  VerifyCheck,
} from '@/transport/transport';
import type { components } from '@/schema/generated';
import type { CredentialName } from '@/dashboard/dashboard-context';

// `MeSettings`/`SecretStatus` aren't among transport.ts's re-exports (only the
// types its own functions traffic in are) — pull them the same way it derives
// its own local aliases, straight off the generated wire schema.
type MeSettings = components['schemas']['MeSettings'];
type SecretStatus = components['schemas']['SecretStatus'];

/** The exact contract string (task-13 e2e binds to it): "configured · …<tail>". */
function secretStatusText(status: SecretStatus): string {
  return status.set ? `configured · …${status.tail}` : 'not configured';
}

interface SecretStatusRowProps {
  name: CredentialName;
  status: SecretStatus;
}

function SecretStatusRow({ name, status }: SecretStatusRowProps): JSX.Element {
  return (
    <span data-role="secret-status" data-name={name} data-set={String(status.set)}>
      {secretStatusText(status)}
    </span>
  );
}

/** Which `VerifyCheck.name` each credential field's indicator reads from —
 * the GitHub token guards repo access, so it maps to the "repo" check rather
 * than a standalone "github" one. */
const CHECK_NAME_FOR_CREDENTIAL: Record<CredentialName, VerifyCheck['name']> = {
  anthropic_api_key: 'anthropic',
  amika_api_key: 'amika',
  github_auth_token: 'repo',
};

type CredentialIndicatorStatus = 'ok' | 'failed' | 'skipped' | 'pending';

/** `pending` covers two windows: this field's own save request, and any
 * verify run at all (one verify call checks all three at once, so every
 * indicator goes pending together while it's in flight) — either means "the
 * last known result can't be trusted yet". Absent any check result, the
 * field reads `skipped` (nothing has verified it — same as an explicit
 * "skipped" check, and rendered the same way: no glyph). */
function credentialIndicatorStatus(
  name: CredentialName,
  pendingCredential: CredentialName | null,
  verifying: boolean,
  check: VerifyCheck | undefined,
): CredentialIndicatorStatus {
  if (pendingCredential === name || verifying) {
    return 'pending';
  }
  return check?.status ?? 'skipped';
}

interface CredentialStatusIndicatorProps {
  name: CredentialName;
  status: CredentialIndicatorStatus;
  message?: string | undefined;
}

/** The right-of-input validity mark: a checkmark once its verify check comes
 * back ok, a cross (with the check's message as a hover title) when it
 * fails, a quiet ellipsis while its save or a verify is in flight, and
 * nothing rendered for `skipped` — `data-status` always carries the real
 * state (tests bind to it), the glyph is just the human-visible layer on
 * top. */
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

export interface ProjectFieldsProps {
  /** Absent in onboarding (no project yet) — every field starts blank. */
  project?: MeProject;
  saving: boolean;
  onSave: (body: ProjectUpdateRequest) => Promise<void>;
}

export function ProjectFields({ project, saving, onSave }: ProjectFieldsProps): JSX.Element {
  const [name, setName] = useState(project?.name ?? '');
  const [repoUrl, setRepoUrl] = useState(project?.repo_url ?? '');
  const [amikaSnapshot, setAmikaSnapshot] = useState(project?.amika_snapshot ?? '');
  const [brainModel, setBrainModel] = useState(project?.brain_model ?? '');
  const [workerCount, setWorkerCount] = useState(
    project?.worker_count === undefined ? '' : String(project.worker_count),
  );

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const body: ProjectUpdateRequest = { name: name.trim(), repo_url: repoUrl.trim() };
    const trimmedSnapshot = amikaSnapshot.trim();
    if (trimmedSnapshot !== '') {
      body.amika_snapshot = trimmedSnapshot;
    }
    const trimmedModel = brainModel.trim();
    if (trimmedModel !== '') {
      body.brain_model = trimmedModel;
    }
    const trimmedWorkerCount = workerCount.trim();
    if (trimmedWorkerCount !== '') {
      const parsed = Number(trimmedWorkerCount);
      if (!Number.isNaN(parsed)) {
        body.worker_count = parsed;
      }
    }
    void onSave(body);
  };

  return (
    <form data-role="project-form" onSubmit={handleSubmit}>
      <label>
        Project name
        <input
          type="text"
          value={name}
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            setName(event.target.value);
          }}
          required
        />
      </label>
      <label>
        Repo URL
        <input
          type="text"
          value={repoUrl}
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            setRepoUrl(event.target.value);
          }}
          required
        />
      </label>
      <label>
        Amika snapshot
        <input
          type="text"
          value={amikaSnapshot}
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            setAmikaSnapshot(event.target.value);
          }}
        />
      </label>
      <label>
        Brain model
        <input
          type="text"
          value={brainModel}
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            setBrainModel(event.target.value);
          }}
        />
      </label>
      <label>
        Worker count
        <input
          type="number"
          value={workerCount}
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            setWorkerCount(event.target.value);
          }}
        />
      </label>
      <button type="submit" disabled={saving}>
        Save project
      </button>
    </form>
  );
}

export interface CredentialFieldsProps {
  settings: MeSettings;
  /** The credential field (if any) whose save/verify is currently in flight —
   * threaded straight through from the store to compute each indicator. */
  pendingCredential: CredentialName | null;
  /** `true` while a verify run is in flight — applies to every indicator at
   * once (one run checks all three fields). */
  verifying: boolean;
  verifyChecks: VerifyCheck[] | null;
  onSave: (body: SettingsUpdateRequest) => Promise<boolean>;
}

export function CredentialFields({
  settings,
  pendingCredential,
  verifying,
  verifyChecks,
  onSave,
}: CredentialFieldsProps): JSX.Element {
  const [anthropicApiKey, setAnthropicApiKey] = useState('');
  const [amikaApiKey, setAmikaApiKey] = useState('');
  const [githubAuthToken, setGithubAuthToken] = useState('');
  const [amikaClaudeCredId, setAmikaClaudeCredId] = useState(settings.amika_claude_cred_id);

  const checkFor = (name: CredentialName): VerifyCheck | undefined =>
    verifyChecks?.find((candidate) => candidate.name === CHECK_NAME_FOR_CREDENTIAL[name]);

  // Fires on blur AND on Enter — never gated behind a submit button anymore.
  // Only sends the one field, only when its draft is non-empty; the draft
  // clears once the save actually succeeds (a failed save leaves the typed
  // value in place rather than silently discarding it).
  const commitAnthropic = (): void => {
    const trimmed = anthropicApiKey.trim();
    if (trimmed === '') {
      return;
    }
    void onSave({ anthropic_api_key: trimmed }).then((succeeded) => {
      if (succeeded) {
        setAnthropicApiKey('');
      }
    });
  };

  const commitAmika = (): void => {
    const trimmed = amikaApiKey.trim();
    if (trimmed === '') {
      return;
    }
    void onSave({ amika_api_key: trimmed }).then((succeeded) => {
      if (succeeded) {
        setAmikaApiKey('');
      }
    });
  };

  const commitGithub = (): void => {
    const trimmed = githubAuthToken.trim();
    if (trimmed === '') {
      return;
    }
    void onSave({ github_auth_token: trimmed }).then((succeeded) => {
      if (succeeded) {
        setGithubAuthToken('');
      }
    });
  };

  // Not a secret — the field just shows the live value, so there's nothing
  // to clear on success; only save when it actually changed.
  const commitCredId = (): void => {
    const trimmed = amikaClaudeCredId.trim();
    if (trimmed === '' || trimmed === settings.amika_claude_cred_id) {
      return;
    }
    void onSave({ amika_claude_cred_id: trimmed });
  };

  const onEnter =
    (commit: () => void) =>
    (event: KeyboardEvent<HTMLInputElement>): void => {
      if (event.key === 'Enter') {
        event.preventDefault();
        commit();
      }
    };

  return (
    <form data-role="settings-form">
      <label>
        Anthropic API key
        <span data-role="credential-input-row">
          <input
            type="password"
            value={anthropicApiKey}
            placeholder={
              settings.anthropic_api_key.set ? secretStatusText(settings.anthropic_api_key) : ''
            }
            onChange={(event: ChangeEvent<HTMLInputElement>) => {
              setAnthropicApiKey(event.target.value);
            }}
            onBlur={commitAnthropic}
            onKeyDown={onEnter(commitAnthropic)}
          />
          <CredentialStatusIndicator
            name="anthropic_api_key"
            status={credentialIndicatorStatus(
              'anthropic_api_key',
              pendingCredential,
              verifying,
              checkFor('anthropic_api_key'),
            )}
            message={checkFor('anthropic_api_key')?.message}
          />
        </span>
      </label>
      <SecretStatusRow name="anthropic_api_key" status={settings.anthropic_api_key} />

      <label>
        Amika API key
        <span data-role="credential-input-row">
          <input
            type="password"
            value={amikaApiKey}
            placeholder={settings.amika_api_key.set ? secretStatusText(settings.amika_api_key) : ''}
            onChange={(event: ChangeEvent<HTMLInputElement>) => {
              setAmikaApiKey(event.target.value);
            }}
            onBlur={commitAmika}
            onKeyDown={onEnter(commitAmika)}
          />
          <CredentialStatusIndicator
            name="amika_api_key"
            status={credentialIndicatorStatus(
              'amika_api_key',
              pendingCredential,
              verifying,
              checkFor('amika_api_key'),
            )}
            message={checkFor('amika_api_key')?.message}
          />
        </span>
      </label>
      <SecretStatusRow name="amika_api_key" status={settings.amika_api_key} />

      <label>
        GitHub token
        <span data-role="credential-input-row">
          <input
            type="password"
            value={githubAuthToken}
            placeholder={
              settings.github_auth_token.set ? secretStatusText(settings.github_auth_token) : ''
            }
            onChange={(event: ChangeEvent<HTMLInputElement>) => {
              setGithubAuthToken(event.target.value);
            }}
            onBlur={commitGithub}
            onKeyDown={onEnter(commitGithub)}
          />
          <CredentialStatusIndicator
            name="github_auth_token"
            status={credentialIndicatorStatus(
              'github_auth_token',
              pendingCredential,
              verifying,
              checkFor('github_auth_token'),
            )}
            message={checkFor('github_auth_token')?.message}
          />
        </span>
      </label>
      <SecretStatusRow name="github_auth_token" status={settings.github_auth_token} />

      <label>
        Amika Claude credential ID
        <input
          type="text"
          value={amikaClaudeCredId}
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            setAmikaClaudeCredId(event.target.value);
          }}
          onBlur={commitCredId}
          onKeyDown={onEnter(commitCredId)}
        />
      </label>
    </form>
  );
}
