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
	AmikaBaseURL      string
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
	CreatedAt     time.Time
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
	AmikaBaseURL      string
	AmikaClaudeCredID string
}

// Me is everything GET /api/me returns (11 §4).
type Me struct {
	User     User
	Project  *Project // nil until onboarding creates it
	Settings MeSettings
}

// SettingsUpdate is a partial credential write; empty string = leave unchanged.
type SettingsUpdate struct {
	AnthropicKey      string
	AmikaKey          string
	GitHubToken       string
	AmikaBaseURL      string
	AmikaClaudeCredID string
}

// ProjectUpdate creates or updates the caller's project.
type ProjectUpdate struct {
	Name          string
	RepoURL       string
	AmikaSnapshot string
	BrainModel    string
	WorkerCount   int
}

// CheckResult is one live connection check (POST /api/settings/verify, 11 §4).
type CheckResult struct {
	Name    string // "anthropic" | "amika" | "repo"
	Status  string // "ok" | "failed" | "skipped"
	Message string
}
