// Controlled forms for the dashboard's two config surfaces (11 §5): project
// (name/repo/snapshot/model/workers) and credentials (secrets + Amika config).
// Both are seeded from the current `Me` and submit only the fields the user
// actually filled in — secrets are write-only (11 §3 D7): the input never
// carries the stored value, only a placeholder built from its status tail.
import { useState, type ChangeEvent, type FormEvent, type JSX } from 'react';
import type { MeProject, ProjectUpdateRequest, SettingsUpdateRequest } from '@/transport/transport';
import type { components } from '@/schema/generated';

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
  name: 'anthropic_api_key' | 'amika_api_key' | 'github_auth_token';
  status: SecretStatus;
}

function SecretStatusRow({ name, status }: SecretStatusRowProps): JSX.Element {
  return (
    <span data-role="secret-status" data-name={name} data-set={String(status.set)}>
      {secretStatusText(status)}
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
  saving: boolean;
  onSave: (body: SettingsUpdateRequest) => Promise<void>;
}

export function CredentialFields({ settings, saving, onSave }: CredentialFieldsProps): JSX.Element {
  const [anthropicApiKey, setAnthropicApiKey] = useState('');
  const [amikaApiKey, setAmikaApiKey] = useState('');
  const [githubAuthToken, setGithubAuthToken] = useState('');
  const [amikaBaseUrl, setAmikaBaseUrl] = useState(settings.amika_base_url);
  const [amikaClaudeCredId, setAmikaClaudeCredId] = useState(settings.amika_claude_cred_id);

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const body: SettingsUpdateRequest = {};
    const trimmedAnthropic = anthropicApiKey.trim();
    if (trimmedAnthropic !== '') {
      body.anthropic_api_key = trimmedAnthropic;
    }
    const trimmedAmikaKey = amikaApiKey.trim();
    if (trimmedAmikaKey !== '') {
      body.amika_api_key = trimmedAmikaKey;
    }
    const trimmedGithub = githubAuthToken.trim();
    if (trimmedGithub !== '') {
      body.github_auth_token = trimmedGithub;
    }
    const trimmedBaseUrl = amikaBaseUrl.trim();
    if (trimmedBaseUrl !== '') {
      body.amika_base_url = trimmedBaseUrl;
    }
    const trimmedCredId = amikaClaudeCredId.trim();
    if (trimmedCredId !== '') {
      body.amika_claude_cred_id = trimmedCredId;
    }
    void onSave(body);
    // Secrets are write-only: never echo the submitted value back into the
    // input — clear the drafts so the placeholder (next render's fresh status)
    // is what shows again.
    setAnthropicApiKey('');
    setAmikaApiKey('');
    setGithubAuthToken('');
  };

  return (
    <form data-role="settings-form" onSubmit={handleSubmit}>
      <label>
        Anthropic API key
        <input
          type="password"
          value={anthropicApiKey}
          placeholder={
            settings.anthropic_api_key.set ? secretStatusText(settings.anthropic_api_key) : ''
          }
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            setAnthropicApiKey(event.target.value);
          }}
        />
      </label>
      <SecretStatusRow name="anthropic_api_key" status={settings.anthropic_api_key} />

      <label>
        Amika API key
        <input
          type="password"
          value={amikaApiKey}
          placeholder={settings.amika_api_key.set ? secretStatusText(settings.amika_api_key) : ''}
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            setAmikaApiKey(event.target.value);
          }}
        />
      </label>
      <SecretStatusRow name="amika_api_key" status={settings.amika_api_key} />

      <label>
        GitHub token
        <input
          type="password"
          value={githubAuthToken}
          placeholder={
            settings.github_auth_token.set ? secretStatusText(settings.github_auth_token) : ''
          }
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            setGithubAuthToken(event.target.value);
          }}
        />
      </label>
      <SecretStatusRow name="github_auth_token" status={settings.github_auth_token} />

      <label>
        Amika base URL
        <input
          type="text"
          value={amikaBaseUrl}
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            setAmikaBaseUrl(event.target.value);
          }}
        />
      </label>
      <label>
        Amika Claude credential ID
        <input
          type="text"
          value={amikaClaudeCredId}
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            setAmikaClaudeCredId(event.target.value);
          }}
        />
      </label>
      <button type="submit" disabled={saving}>
        Save credentials
      </button>
    </form>
  );
}
