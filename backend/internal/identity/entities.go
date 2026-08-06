package identity

import "time"

// User is a signed-up GitHub identity (11 §3).
type User struct {
	ID          string
	GitHubID    int64
	GitHubLogin string // stored lower-cased
	DisplayName string
	AvatarURL   string
	CreatedAt   time.Time
}

// Session is a server-side cookie session; only the token's hash is stored (11 §2).
type Session struct {
	TokenHash string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// UserConfig is the encrypted-at-rest credentials row (11 §3 D4, D7).
// *Enc fields hold AES-GCM ciphertext (nil = unset); the two non-secret
// fields are stored in the clear.
type UserConfig struct {
	UserID string
	// AnthropicKeyEnc is DORMANT (global-key change): the brain now drives a
	// deployment-global ANTHROPIC_API_KEY env setting, so this column/field is
	// no longer consumed at runtime. Kept (not dropped) so per-user Anthropic
	// keys can be brought back when user management expands — re-enabling needs
	// no migration.
	AnthropicKeyEnc []byte
	AmikaKeyEnc     []byte
	// DevinKeyEnc is the Devin bearer token (a `cog_…` service key or PAT),
	// AES-GCM ciphertext (nil = unset). Per-user like AmikaKeyEnc: a project
	// that selects the Devin provider authenticates with its owner's stored key
	// (multi-provider design §8), falling back to the deployment DEVIN_API_KEY
	// env when unset so existing opt-in deployments are unchanged.
	DevinKeyEnc []byte
	// GitHubInstallationID names the GitHub App installation this user granted
	// (0 = none). It is an IDENTIFIER, NOT A SECRET — it says which installation
	// to mint against, and possession of it authorizes nothing without the App's
	// private key. That is the whole shape of the App migration: the long-lived
	// secret this column replaced is gone, and what is left in its place is safe
	// to store in the clear, log, and put on the wire.
	GitHubInstallationID int64
	// GitHubInstallationRevokedAt records the moment a mint against
	// GitHubInstallationID came back 401/403/404 — the App was uninstalled,
	// suspended, or its access withdrawn. Nil (the common case) means "no
	// evidence of trouble"; set means the card reads needs_reconnect and offers
	// the install flow again. Recorded rather than re-derived so GET /api/me
	// stays a pure DB read: asking GitHub on every page load would turn a
	// dashboard render into a network round trip.
	GitHubInstallationRevokedAt *time.Time
	// GitHubTokenEnc is the USER access token from the App's user-authorization
	// half — the credential that answers "which of this installation's repos can
	// THIS person see" for the repo picker. It is not the repo credential: git
	// and `gh` authenticate with an installation token minted on demand
	// (InstallationTokens), never with this.
	//
	// It doubles as the carry-forward slot for a credential Kiln did not mint —
	// a hand-typed PAT through PUT /api/settings, or the bootstrap GITHUB_AUTH_TOKEN
	// — which is why a user with no installation but a stored token still reads
	// as connected-with-unknown-provenance rather than disconnected.
	GitHubTokenEnc []byte
	// GitHubConnectedLogin is the GitHub account GitHubTokenEnc belongs to,
	// recorded at connect time. Empty for a hand-typed/bootstrap token (nobody
	// recorded whose it was), which is why the card falls back to the session's
	// own login rather than claiming an account it cannot vouch for.
	GitHubConnectedLogin string
	AmikaClaudeCredID    string
}

// MergeGateMode names which condition satisfies a ticket's merge gate (06 §7).
// The zero value ("") is treated as MergeGateMain so existing projects keep the
// original behavior without a data backfill.
type MergeGateMode string

const (
	// MergeGateMain accepts a ticket done only once its commit is on origin/main.
	MergeGateMain MergeGateMode = "main"
	// MergeGatePR accepts a ticket done once the work exists in a pull request,
	// merged or not.
	MergeGatePR MergeGateMode = "pr"
)

// Project parameterizes one brain/board (11 §3 D5): the repo it works on and
// the brain-shaped knobs. One per user in phase 1.
type Project struct {
	ID          string
	OwnerUserID string
	Name        string
	RepoURL     string
	// AgentProvider is the registry key (amika, devin, mock, …) that selects which
	// coding-agent provider this project's turns run on (multi-provider design §9,
	// D7). Empty means "use the deployment default" (AGENT_MODE) — the back-compat
	// behavior every existing project keeps with no backfill. Identity stores it
	// opaquely; the composition root validates it against the registered set.
	AgentProvider string
	AmikaSnapshot string
	WorkerCount   int
	MergeGateMode MergeGateMode
	AmikaSecrets  []AmikaSecret
	CreatedAt     time.Time
}

// AmikaSecret is one project secret injected into every sandbox this project
// starts (02 §8): Name is the environment variable it lands under, Value is the
// secret. BOTH are stored AES-GCM-encrypted at rest (D7) — this struct only ever
// holds ciphertext. The name is decrypted for display (it is a label, not a
// secret); the value is write-only and only ever leaves as a fingerprint.
type AmikaSecret struct {
	NameEnc  []byte
	ValueEnc []byte
}

// AmikaSecretInput is one inbound secret on a project upsert (02 §8). Value is
// write-only: empty means "keep the stored value for this name" (the credential
// merge convention, 11 §3 D7); non-empty replaces it. A name absent from the
// list is cleared.
type AmikaSecretInput struct {
	Name  string
	Value string
}

// AmikaSecretStatus is the fingerprint-only read view of one stored secret: the
// decrypted Name plus the value's presence+tail (never the value).
type AmikaSecretStatus struct {
	Name  string
	Value SecretStatus
}

// AmikaSecretValue is one decrypted secret for in-process sandbox injection
// (RuntimeConfig only — plaintext, never wire/logged).
type AmikaSecretValue struct {
	Name  string
	Value string
}

// SecretStatus is the fingerprint-only read shape for a stored secret (11 §3 D7).
type SecretStatus struct {
	Set  bool
	Tail string
}

// Repo is one repository the caller's connected GitHub account can reach — the
// domain shape behind the settings repo picker. URL is the https web URL
// (`https://github.com/owner/name`), which is exactly what a project stores as
// its RepoURL, so picking a repo and saving it is a straight assignment.
type Repo struct {
	FullName string // owner/name — the label the picker lists
	URL      string
	Private  bool
}

// MeSettings is the config-status view: never secret values.
type MeSettings struct {
	AnthropicKey SecretStatus
	AmikaKey     SecretStatus
	DevinKey     SecretStatus
	GitHubToken  SecretStatus
	// GitHub is the derived state the Integrations card renders — presence AND
	// sufficiency of the repo credential, which SecretStatus alone can't say.
	GitHub            GitHubConnection
	AmikaClaudeCredID string
}

// The four states of a user's GitHub repo credential. They exist because "a
// credential is on file" and "that credential can actually reach the repo" are
// different questions, and the Integrations card's Connect button has to say
// which. Under the GitHub App the discriminator is the INSTALLATION, not a
// scope string: an installation is a live grant GitHub can withdraw, so its
// presence and its health are the two facts worth recording.
const (
	// GitHubDisconnected: no installation and no stored credential at all.
	GitHubDisconnected = "disconnected"
	// GitHubUnknownScopes: a credential is stored but no installation is —
	// a hand-typed PAT, or the deployment's bootstrap GITHUB_AUTH_TOKEN.
	// Treated as WORKING (it was configured deliberately, and a fine-grained
	// PAT is a perfectly good repo credential), never as broken; it simply is
	// not something Kiln granted itself and so cannot vouch for.
	GitHubUnknownScopes = "unknown"
	// GitHubNeedsReconnect: an installation is recorded and GitHub rejected a
	// mint against it — uninstalled, suspended, or access withdrawn. This is
	// the "don't leave it silently broken" state: the card prompts a re-install
	// through the same connect flow instead of failing at the next agent turn.
	GitHubNeedsReconnect = "needs_reconnect"
	// GitHubConnected: an installation is recorded and nothing has reported it
	// dead.
	GitHubConnected = "connected"
)

// GitHubConnection is the account view's read of the repo credential.
type GitHubConnection struct {
	Status string
	// Login is the account the credential belongs to; empty when unrecorded
	// (a hand-typed or bootstrap token).
	Login string
	// InstallationID names the GitHub App installation backing this connection,
	// 0 when there is none. Safe on the wire — it identifies, it does not
	// authorize (see UserConfig.GitHubInstallationID).
	InstallationID int64
	// ConfigureURL is GitHub's own installation-settings page, where the user
	// changes WHICH repositories Kiln may touch. Kiln links out rather than
	// reimplementing that screen — GitHub owns the repository chooser, and it is
	// the only place the choice can actually be made. Empty when there is no
	// installation to configure.
	ConfigureURL string
}

// ProjectView is one project plus its fingerprint-only secret statuses
// (decrypted names + value presence). Deriving Secrets needs the cipher, which
// the api's wire mapping doesn't hold, so the service computes it here (12 §3.1).
type ProjectView struct {
	Project Project
	Secrets []AmikaSecretStatus
}

// Me is everything GET /api/me returns (11 §4, 12 §3.1). Projects is the user's
// live project set (empty until onboarding creates the first) — the collection
// that replaced the old singular Project?, so "not onboarded" is len==0 (12 §4.1).
type Me struct {
	User     User
	Projects []ProjectView
	Settings MeSettings
}

// SettingsUpdate is a partial credential write; empty string = leave unchanged.
type SettingsUpdate struct {
	AnthropicKey      string
	AmikaKey          string
	DevinKey          string
	GitHubToken       string
	AmikaClaudeCredID string
}

// ProjectUpdate creates or updates the caller's project. Like the rest of this
// wholesale upsert, AmikaSecrets replaces the stored list outright (nil clears
// it); an entry with an empty Value keeps that name's stored value.
type ProjectUpdate struct {
	Name          string
	RepoURL       string
	AgentProvider string // registry key selecting the project's provider; empty ⇒ deployment default (design §9)
	AmikaSnapshot string
	WorkerCount   int
	MergeGateMode MergeGateMode
	AmikaSecrets  []AmikaSecretInput
}

// CheckResult is one live connection check (POST /api/settings/verify, 11 §4).
type CheckResult struct {
	Name    string // "anthropic" | "amika" | "devin" | "repo"
	Status  string // "ok" | "failed" | "skipped"
	Message string
}
