package githubmock_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity/githubapi"
	"github.com/crabtree-michael/kiln/backend/internal/identity/githubmock"
)

// gitHubApp is the App-half method set, declared here so BOTH the real adapter
// and the mock must satisfy it.
//
// This is the point of the file. The OAuth half is already compile-checked —
// cmd/kiln's newGitHub returns identity.GitHub, so a drifting mock breaks the
// build. Nothing enforces that for the App half until identity's port widens in
// step 3, and a mock that silently stops matching the real adapter is exactly
// how a keyless stack starts passing tests the real one would fail.
type gitHubApp interface {
	InstallURL(state string) string
	MintInstallationToken(ctx context.Context, installationID int64) (githubapi.InstallationToken, error)
	ListInstallationRepos(ctx context.Context, accessToken string, installationID int64) ([]githubapi.Repo, error)
}

var (
	_ gitHubApp = (*githubapi.Client)(nil)
	_ gitHubApp = (*githubmock.Client)(nil)
)

// The mock exists so a keyless stack never reaches github.com. An install URL
// that escaped to a real host would send a keyless run out to the internet at
// the exact moment it is meant to be offline.
func TestInstallURLStaysLocal(t *testing.T) {
	got := githubmock.New().InstallURL("state-abc")

	if !strings.HasPrefix(got, "/auth/github/callback?") {
		t.Errorf("InstallURL = %q, want a local /auth/github/callback URL", got)
	}
	if strings.Contains(got, "github.com") {
		t.Errorf("InstallURL = %q, must not point at a real GitHub host", got)
	}
	// The callback's new parameters (design §3.2) must be present, since
	// exercising Kiln's handling of them is what this URL is for.
	for _, want := range []string{"state=state-abc", "installation_id=4242", "setup_action=install"} {
		if !strings.Contains(got, want) {
			t.Errorf("InstallURL = %q, want it to carry %q", got, want)
		}
	}
}

// The canned mint has to look enough like a real one that a caller's cache logic
// is genuinely exercised: a live token, an expiry in the future, and the
// narrowed selection this migration is about.
func TestMintInstallationToken(t *testing.T) {
	before := time.Now()

	got, err := githubmock.New().MintInstallationToken(t.Context(), githubmock.MockInstallationID)
	if err != nil {
		t.Fatalf("MintInstallationToken: %v", err)
	}

	if got.Token != githubmock.MockInstallationToken {
		t.Errorf("Token = %q, want %q", got.Token, githubmock.MockInstallationToken)
	}
	// An expiry in the past would make every caller re-mint forever; one that
	// never arrives would leave refresh-before-expiry untested offline.
	if !got.ExpiresAt.After(before) {
		t.Errorf("ExpiresAt = %v, want a future expiry (now %v)", got.ExpiresAt, before)
	}
	if got.ExpiresAt.After(before.Add(2 * time.Hour)) {
		t.Errorf("ExpiresAt = %v, want roughly GitHub's one-hour window", got.ExpiresAt)
	}
	if got.RepositorySelection != githubapi.RepositorySelectionSelected {
		t.Errorf("RepositorySelection = %q, want selected", got.RepositorySelection)
	}
}

// Any installation id mints, the same way any token lists repos: the mock
// classifies a keyless user as connected rather than modelling GitHub's state.
func TestMintInstallationTokenAcceptsAnyInstallation(t *testing.T) {
	if _, err := githubmock.New().MintInstallationToken(t.Context(), 999); err != nil {
		t.Errorf("MintInstallationToken(999) = %v, want nil", err)
	}
}

// Both listings return the same canned set. The assertion is the point: it
// records that the mock deliberately does NOT invent a narrowed subset, so
// nobody later "fixes" the difference and has the keyless lane assert on fiction.
func TestListInstallationReposMatchesListRepos(t *testing.T) {
	c := githubmock.New()

	installed, err := c.ListInstallationRepos(t.Context(), githubmock.MockToken, githubmock.MockInstallationID)
	if err != nil {
		t.Fatalf("ListInstallationRepos: %v", err)
	}
	all, err := c.ListRepos(t.Context(), githubmock.MockToken)
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}

	if len(installed) != len(all) {
		t.Fatalf("len(ListInstallationRepos) = %d, len(ListRepos) = %d, want equal", len(installed), len(all))
	}
	for i := range installed {
		if installed[i] != all[i] {
			t.Errorf("repos[%d] = %+v, want %+v", i, installed[i], all[i])
		}
	}
	// Offline means offline: a keyless clone must not be able to reach a real
	// repository through the picker.
	for _, r := range installed {
		if strings.Contains(r.HTMLURL, "github.com") {
			t.Errorf("repo %q has a real GitHub URL %q", r.FullName, r.HTMLURL)
		}
	}
}
