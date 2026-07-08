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
	UserID            string
	AnthropicKeyEnc   []byte
	AmikaKeyEnc       []byte
	GitHubTokenEnc    []byte
	AmikaClaudeCredID string
}

// Project parameterizes one brain/board (11 §3 D5): the repo it works on and
// the brain-shaped knobs. One per user in phase 1.
type Project struct {
	ID            string
	OwnerUserID   string
	Name          string
	RepoURL       string
	AmikaSnapshot string
	BrainModel    string
	WorkerCount   int
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

// MeSettings is the config-status view: never secret values.
type MeSettings struct {
	AnthropicKey      SecretStatus
	AmikaKey          SecretStatus
	GitHubToken       SecretStatus
	AmikaClaudeCredID string
}

// Me is everything GET /api/me returns (11 §4).
type Me struct {
	User    User
	Project *Project // nil until onboarding creates it
	// ProjectSecrets is the fingerprint-only view of Project.AmikaSecrets
	// (decrypted names + value status), nil when there is no project. Kept
	// beside Project because deriving it needs the cipher, which the api's
	// wire mapping doesn't hold.
	ProjectSecrets []AmikaSecretStatus
	Settings       MeSettings
}

// SettingsUpdate is a partial credential write; empty string = leave unchanged.
type SettingsUpdate struct {
	AnthropicKey      string
	AmikaKey          string
	GitHubToken       string
	AmikaClaudeCredID string
}

// ProjectUpdate creates or updates the caller's project. Like the rest of this
// wholesale upsert, AmikaSecrets replaces the stored list outright (nil clears
// it); an entry with an empty Value keeps that name's stored value.
type ProjectUpdate struct {
	Name          string
	RepoURL       string
	AmikaSnapshot string
	BrainModel    string
	WorkerCount   int
	AmikaSecrets  []AmikaSecretInput
}

// CheckResult is one live connection check (POST /api/settings/verify, 11 §4).
type CheckResult struct {
	Name    string // "anthropic" | "amika" | "repo"
	Status  string // "ok" | "failed" | "skipped"
	Message string
}
