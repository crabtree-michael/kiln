package identity_test

// Unit tests for the phase-2 runtime bridge on identity.Service (spec 11
// phase 2): RuntimeConfig credential resolution, EnsureUser find-or-create,
// the SetInvalidator registry hook, and the project-listing helpers. These
// exercise Service directly against fakeStore (fakes_test.go) — the decrypt
// round-trip goes through the real Cipher, never a mock.

import (
	"context"
	"errors"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// mustUpsertProject seeds a project for userID and returns its assigned id.
func mustUpsertProject(t *testing.T, svc *identity.Service, userID string) identity.Project {
	t.Helper()
	p, err := svc.UpsertProject(context.Background(), userID, identity.ProjectUpdate{
		Name:    testProjectName,
		RepoURL: testProjectRepoURL,
	})
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	return p
}

func TestRuntimeConfigDecryptsRoundTrip(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "runtime-user")

	const (
		anthropic = "sk-ant-runtimeKEY1"
		amika     = "amk-runtime-2222"
		ghToken   = "ghp-runtime-token-3333" //nolint:gosec // test fixture, not a real credential
		credID    = "amika-cred-42"          //nolint:gosec // test fixture, not a real credential
	)
	if err := svc.UpdateSettings(context.Background(), u.ID, identity.SettingsUpdate{
		AnthropicKey:      anthropic,
		AmikaKey:          amika,
		GitHubToken:       ghToken,
		AmikaClaudeCredID: credID,
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	proj := mustUpsertProject(t, svc, u.ID)

	rc, err := svc.RuntimeConfig(context.Background(), proj.ID)
	if err != nil {
		t.Fatalf("RuntimeConfig: %v", err)
	}
	if rc.OwnerUserID != u.ID {
		t.Fatalf("OwnerUserID = %q, want %q", rc.OwnerUserID, u.ID)
	}
	if rc.Project.ID != proj.ID {
		t.Fatalf("Project.ID = %q, want %q", rc.Project.ID, proj.ID)
	}
	if rc.AnthropicAPIKey != anthropic {
		t.Fatalf("AnthropicAPIKey = %q, want decrypted %q", rc.AnthropicAPIKey, anthropic)
	}
	if rc.AmikaAPIKey != amika {
		t.Fatalf("AmikaAPIKey = %q, want decrypted %q", rc.AmikaAPIKey, amika)
	}
	// The GitHub credential is a source, not a value: with no installation
	// behind it, resolving it yields the decrypted stored token verbatim.
	resolved, err := rc.GitHubToken(context.Background())
	if err != nil {
		t.Fatalf("resolve GitHubToken: %v", err)
	}
	if resolved != ghToken {
		t.Fatalf("GitHubToken = %q, want decrypted %q", resolved, ghToken)
	}
	if rc.AmikaClaudeCredID != credID {
		t.Fatalf("AmikaClaudeCredID = %q, want %q", rc.AmikaClaudeCredID, credID)
	}
}

func TestRuntimeConfigUnsetColumnsAreEmpty(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "runtime-empty-user")
	proj := mustUpsertProject(t, svc, u.ID) // no UpdateSettings: config all-unset

	rc, err := svc.RuntimeConfig(context.Background(), proj.ID)
	if err != nil {
		t.Fatalf("RuntimeConfig: %v", err)
	}
	if rc.AnthropicAPIKey != "" || rc.AmikaAPIKey != "" || rc.AmikaClaudeCredID != "" {
		t.Fatalf("unset credentials must decrypt to empty, got %+v", rc)
	}
	// An unset GitHub credential resolves to "" rather than erroring: not being
	// connected is an ordinary state, and the caller renders a prompt for it.
	switch resolved, rerr := rc.GitHubToken(context.Background()); {
	case rerr != nil:
		t.Fatalf("resolve GitHubToken: %v", rerr)
	case resolved != "":
		t.Fatalf("GitHubToken = %q, want empty", resolved)
	}
	if rc.OwnerUserID != u.ID {
		t.Fatalf("OwnerUserID = %q, want %q", rc.OwnerUserID, u.ID)
	}
}

func TestRuntimeConfigUnknownProject(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)

	if _, err := svc.RuntimeConfig(context.Background(), "no-such-project"); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("RuntimeConfig(unknown) err = %v, want ErrNotFound", err)
	}
}

func TestSetInvalidatorFiresOnUpsertProject(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "invalidate-project-user")

	var fired []string
	svc.SetInvalidator(func(projectID string) { fired = append(fired, projectID) })

	proj := mustUpsertProject(t, svc, u.ID)
	if len(fired) != 1 || fired[0] != proj.ID {
		t.Fatalf("invalidator fired = %v, want [%q]", fired, proj.ID)
	}
}

func TestSetInvalidatorFiresOnUpdateSettings(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "invalidate-settings-user")
	proj := mustUpsertProject(t, svc, u.ID)

	var fired []string
	svc.SetInvalidator(func(projectID string) { fired = append(fired, projectID) })

	if err := svc.UpdateSettings(context.Background(), u.ID, identity.SettingsUpdate{
		AnthropicKey: "sk-ant-newKEY",
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if len(fired) != 1 || fired[0] != proj.ID {
		t.Fatalf("invalidator fired = %v, want [%q] (owner's project)", fired, proj.ID)
	}
}

func TestUpdateSettingsNoProjectDoesNotInvalidate(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "no-project-user") // never onboarded

	var fired []string
	svc.SetInvalidator(func(projectID string) { fired = append(fired, projectID) })

	if err := svc.UpdateSettings(context.Background(), u.ID, identity.SettingsUpdate{
		AnthropicKey: "sk-ant-orphanKEY",
	}); err != nil {
		t.Fatalf("UpdateSettings with no project must not error: %v", err)
	}
	if len(fired) != 0 {
		t.Fatalf("invalidator fired = %v, want none (user has no project yet)", fired)
	}
}

func TestEnsureUserIdempotent(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)

	first, err := svc.EnsureUser(context.Background(), "Ensure-User")
	if err != nil {
		t.Fatalf("EnsureUser first: %v", err)
	}
	if first.ID == "" {
		t.Fatal("EnsureUser returned empty id")
	}
	if first.GitHubLogin != "ensure-user" {
		t.Fatalf("GitHubLogin = %q, want lower-cased ensure-user", first.GitHubLogin)
	}

	second, err := svc.EnsureUser(context.Background(), "ensure-user")
	if err != nil {
		t.Fatalf("EnsureUser second: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second EnsureUser id = %q, want %q (find-or-create)", second.ID, first.ID)
	}
	if n := store.userCount(); n != 1 {
		t.Fatalf("store has %d users, want 1 (idempotent)", n)
	}
}

func TestListProjectIDs(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)

	u1 := mustDevSignIn(t, svc, "list-user-1")
	u2 := mustDevSignIn(t, svc, "list-user-2")
	p1 := mustUpsertProject(t, svc, u1.ID)
	p2 := mustUpsertProject(t, svc, u2.ID)

	ids, err := svc.ListProjectIDs(context.Background())
	if err != nil {
		t.Fatalf("ListProjectIDs: %v", err)
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if len(ids) != 2 || !got[p1.ID] || !got[p2.ID] {
		t.Fatalf("ListProjectIDs = %v, want both %q and %q", ids, p1.ID, p2.ID)
	}
}

func TestProjectForWrapsGetProjectByOwner(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "projectfor-user")

	if _, err := svc.ProjectFor(context.Background(), u.ID); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("ProjectFor before onboarding = %v, want ErrNotFound", err)
	}
	proj := mustUpsertProject(t, svc, u.ID)
	got, err := svc.ProjectFor(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("ProjectFor: %v", err)
	}
	if got.ID != proj.ID {
		t.Fatalf("ProjectFor id = %q, want %q", got.ID, proj.ID)
	}
}
