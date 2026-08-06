// The provider/credential facts shared by the two surfaces that connect a
// provider: the settings Integrations cards, and the guided setup flow's
// provider step (`Onboarding`).
//
// It is a separate, JSX-free module for the same reason `dashboard-context.ts`
// and `secret-status.ts` are: a module that exports components cannot also
// export constants without tripping react-refresh/only-export-components. Put
// anything both surfaces need here rather than importing it out of
// `Integrations.tsx`.
import type { SettingsUpdateRequest, VerifyCheck } from '@/transport/transport';
import type { CredentialName } from '@/dashboard/dashboard-context';

/** The credentials a user pastes as a key. GitHub is deliberately absent: it is
 * connected through OAuth, not by pasting a token. */
export type ApiKeyCredential = Exclude<CredentialName, 'github_auth_token'>;

/** The `SettingsUpdateRequest` keys that are write-only secrets with their own
 * verify check and indicator — as opposed to `amika_claude_cred_id`, which is
 * plain text and never chains a verify run. */
export const CREDENTIAL_KEYS: readonly CredentialName[] = [
  'anthropic_api_key',
  'amika_api_key',
  'devin_api_key',
  'github_auth_token',
];

/** Which single credential field (if any) a partial `SettingsUpdateRequest`
 * body is writing — each auto-save commits exactly one field, so at most one
 * ever matches.
 *
 * Lives here rather than in `dashboard-store.tsx` because a second store now
 * answers the same question: the sign-up rehearsal (`/signup`) drives the real
 * flow against a simulated store, and it has to mark the same field pending and
 * chain the same verify run, or the rehearsal stops rehearsing the thing it
 * exists to show. */
export function credentialKeyIn(body: SettingsUpdateRequest): CredentialName | null {
  for (const key of CREDENTIAL_KEYS) {
    const value = body[key];
    if (typeof value === 'string' && value !== '') {
      return key;
    }
  }
  return null;
}

/** Which `VerifyCheck.name` each credential's indicator reads from — GitHub's
 * connection guards repo access, so it maps to the "repo" check rather than a
 * standalone "github" one. */
export const CHECK_NAME_FOR_CREDENTIAL: Record<CredentialName, VerifyCheck['name']> = {
  anthropic_api_key: 'anthropic',
  amika_api_key: 'amika',
  devin_api_key: 'devin',
  github_auth_token: 'repo',
};

// GITHUB_CONNECT_PATH is deliberately NOT here — it lives in
// `@/auth/github-connect`. This module is dashboard-scoped and carries provider
// tables and request builders, but the connect route is reached from the landing
// page and the app's session gate too; those must not pull the dashboard's
// credential machinery into their bundle to link to it. Import it from there.

/** What connecting actually authorizes, stated wherever we ask for it so the
 * `repo` grant on GitHub's consent screen is expected rather than alarming. */
export const GITHUB_ACCESS_NOTE =
  'Connecting grants Kiln read and write access to your repositories — it needs ' +
  'that to clone your repo, read it, and push the branches your agents produce.';

export interface ApiKeyProvider {
  /** The settings field this writes, and the key its indicator reads. */
  credential: ApiKeyCredential;
  /** `data-provider` on the card — the stable selector tests/e2e bind to. Also
   * the agent-provider registry key, which is what lets the setup flow look a
   * provider's key up from the descriptor the user just chose. */
  provider: string;
  /** Card heading. */
  title: string;
  /** The key input's label. Kept identical to the pre-card field label so the
   * accessible name (`getByLabelText('Amika API key')`) is unchanged. */
  label: string;
  /** The provider's brand mark under `/logos` — the same asset the landing
   * page's agent orbit uses, so the two surfaces name a provider with the same
   * picture. Optional: a provider we hold no mark for falls back to its
   * initial rather than blocking the card. */
  logo?: string;
}

/** The API-key providers, in card render order. Anthropic leads only when its
 * retained field is switched back on (`VITE_SHOW_ANTHROPIC_KEY_FIELD`). */
export const API_KEY_PROVIDERS: ApiKeyProvider[] = [
  {
    credential: 'anthropic_api_key',
    provider: 'anthropic',
    title: 'Anthropic',
    label: 'Anthropic API key',
  },
  {
    credential: 'amika_api_key',
    provider: 'amika',
    title: 'Amika',
    label: 'Amika API key',
    logo: '/logos/amika.svg',
  },
  {
    credential: 'devin_api_key',
    provider: 'devin',
    title: 'Devin',
    label: 'Devin API key',
    logo: '/logos/devin.svg',
  },
];

/** The API-key provider serving a coding-agent registry key (`amika`, `devin`,
 * `mock`, …), or `undefined` when that provider takes no pasted key at all.
 *
 * This is the one place the two vocabularies meet. The dashboard is otherwise
 * data-driven about coding-agent providers — the setup flow renders its choices
 * from `me.providers`, naming none of them — but the credential slots are
 * provider-named in the wire schema (`amika_api_key`, `devin_api_key`), so the
 * join has to happen somewhere. A provider with no match (the in-memory `mock`,
 * or any future provider that authenticates some other way) simply gets no key
 * prompt, which is what keeps this from becoming a gate on adding a provider. */
export function apiKeyProviderFor(providerKey: string): ApiKeyProvider | undefined {
  return API_KEY_PROVIDERS.find((candidate) => candidate.provider === providerKey);
}

/** The one-field update body for a key. A `switch` rather than a computed key
 * so the result is a real `SettingsUpdateRequest`, not an index signature
 * (TS escape hatches are banned, 02 §4b). */
export function updateBodyFor(credential: ApiKeyCredential, value: string): SettingsUpdateRequest {
  switch (credential) {
    case 'anthropic_api_key':
      return { anthropic_api_key: value };
    case 'amika_api_key':
      return { amika_api_key: value };
    case 'devin_api_key':
      return { devin_api_key: value };
  }
}

export type CredentialIndicatorStatus = 'ok' | 'failed' | 'skipped' | 'pending';

/** `pending` covers two windows: this card's own save request (it stays in
 * `pendingCredentials` for the whole save + chained-verify span, independent
 * of any other card's in-flight save), and any verify run at all (one verify
 * call checks every provider at once, so all the indicators go pending
 * together while it's in flight) — either means "the last known result can't
 * be trusted yet". Absent any check result, the card reads `skipped` (nothing
 * has verified it — same as an explicit "skipped" check, and rendered the same
 * way: no glyph). */
export function credentialIndicatorStatus(
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
