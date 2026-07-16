package identity_test

// Unit tests for multi-project support on identity.Service (spec 12): distinct
// creates, the owner-authorizing ProjectByID/UpdateProject/VerifyProject/
// SoftDeleteProject boundary, and Me carrying a projects[] collection. These run
// against fakeStore (fakes_test.go) — no database.

import (
	"context"
	"errors"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// mustCreateProject creates a named project for userID and returns it.
func mustCreateProject(t *testing.T, svc *identity.Service, userID, name string) identity.Project {
	t.Helper()
	v, err := svc.CreateProject(context.Background(), userID, identity.ProjectUpdate{
		Name:    name,
		RepoURL: "https://github.com/x/" + name,
	})
	if err != nil {
		t.Fatalf("CreateProject(%s): %v", name, err)
	}
	return v.Project
}

// TestCreateProjectDistinctAndMeCollection asserts a single owner can hold many
// projects (12 §2), each with a distinct id, and Me returns them oldest-first
// (12 §3.1) — the replacement for the old singular project?.
func TestCreateProjectDistinctAndMeCollection(t *testing.T) {
	svc := identity.NewService(newFakeStore(), mustCipher(t), &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "many-user")

	p1 := mustCreateProject(t, svc, u.ID, "first")
	p2 := mustCreateProject(t, svc, u.ID, "second")
	if p1.ID == p2.ID {
		t.Fatalf("two creates share id %s, want distinct", p1.ID)
	}

	me, err := svc.Me(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if len(me.Projects) != 2 {
		t.Fatalf("Me().Projects = %d, want 2", len(me.Projects))
	}
	if me.Projects[0].Project.ID != p1.ID || me.Projects[1].Project.ID != p2.ID {
		t.Fatalf("Me().Projects order = [%s %s], want [%s %s]",
			me.Projects[0].Project.ID, me.Projects[1].Project.ID, p1.ID, p2.ID)
	}
}

// TestProjectByIDAuthorizesOwner asserts the owner check (12 §3.2): the owner
// resolves their project, a non-owner gets ErrNotFound (never confirming its
// existence), and an unknown id is indistinguishable from a foreign one.
func TestProjectByIDAuthorizesOwner(t *testing.T) {
	svc := identity.NewService(newFakeStore(), mustCipher(t), &fakeGitHub{}, nil)
	owner := mustDevSignIn(t, svc, "owner")
	foreign := mustDevSignIn(t, svc, "foreign")
	p := mustCreateProject(t, svc, owner.ID, "owned")

	got, err := svc.ProjectByID(context.Background(), owner.ID, p.ID)
	if err != nil || got.ID != p.ID {
		t.Fatalf("owner ProjectByID = (%+v, %v), want the project", got, err)
	}
	if _, err := svc.ProjectByID(context.Background(), foreign.ID, p.ID); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("foreign ProjectByID err = %v, want ErrNotFound", err)
	}
	if _, err := svc.ProjectByID(context.Background(), owner.ID, "no-such-id"); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("unknown ProjectByID err = %v, want ErrNotFound", err)
	}
}

// TestUpdateProjectForeignRejected asserts a non-owner cannot update another
// user's project (12 §3.2): ErrNotFound, and the project is unchanged.
func TestUpdateProjectForeignRejected(t *testing.T) {
	svc := identity.NewService(newFakeStore(), mustCipher(t), &fakeGitHub{}, nil)
	owner := mustDevSignIn(t, svc, "owner")
	foreign := mustDevSignIn(t, svc, "foreign")
	p := mustCreateProject(t, svc, owner.ID, "owned")

	if _, err := svc.UpdateProject(context.Background(), foreign.ID, p.ID, identity.ProjectUpdate{
		Name: "hijacked", RepoURL: "https://x/y",
	}); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("foreign UpdateProject err = %v, want ErrNotFound", err)
	}
	got, err := svc.ProjectByID(context.Background(), owner.ID, p.ID)
	if err != nil || got.Name != "owned" {
		t.Fatalf("project after foreign update = (%+v, %v), want intact", got, err)
	}
}

// TestSoftDeleteProjectRemovesFromReads asserts delete's row retirement (12 DP6):
// after SoftDeleteProject the project vanishes from Me and ProjectByID, and a
// foreign delete is refused.
func TestSoftDeleteProjectRemovesFromReads(t *testing.T) {
	svc := identity.NewService(newFakeStore(), mustCipher(t), &fakeGitHub{}, nil)
	owner := mustDevSignIn(t, svc, "owner")
	foreign := mustDevSignIn(t, svc, "foreign")
	p := mustCreateProject(t, svc, owner.ID, "owned")

	if err := svc.SoftDeleteProject(context.Background(), foreign.ID, p.ID); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("foreign SoftDeleteProject err = %v, want ErrNotFound", err)
	}
	if err := svc.SoftDeleteProject(context.Background(), owner.ID, p.ID); err != nil {
		t.Fatalf("owner SoftDeleteProject: %v", err)
	}
	if _, err := svc.ProjectByID(context.Background(), owner.ID, p.ID); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("ProjectByID after delete err = %v, want ErrNotFound", err)
	}
	me, err := svc.Me(context.Background(), owner.ID)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if len(me.Projects) != 0 {
		t.Fatalf("Me().Projects after delete = %d, want 0", len(me.Projects))
	}
}

// TestVerifyProjectUsesTargetRepo asserts the per-project verify (12 §3.1): the
// repo check runs against the named project's repo url, and a foreign project is
// ErrNotFound. The credential checks stay per-user.
func TestVerifyProjectUsesTargetRepo(t *testing.T) {
	svc := identity.NewService(newFakeStore(), mustCipher(t), &fakeGitHub{}, nil)
	verifier := &fakeVerifier{}
	svc.SetVerifier(verifier)
	owner := mustDevSignIn(t, svc, "owner")
	foreign := mustDevSignIn(t, svc, "foreign")

	// Give the owner a repo token so the repo check actually runs (not skipped).
	if err := svc.UpdateSettings(context.Background(), owner.ID, identity.SettingsUpdate{
		GitHubToken: "ghp_token",
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	p := mustCreateProject(t, svc, owner.ID, "reponame")

	checks, err := svc.VerifyProject(context.Background(), owner.ID, p.ID)
	if err != nil {
		t.Fatalf("VerifyProject: %v", err)
	}
	if len(checks) == 0 {
		t.Fatal("VerifyProject returned no checks")
	}
	if verifier.gotRepoURL != p.RepoURL {
		t.Fatalf("verifier saw repo %q, want the project's %q", verifier.gotRepoURL, p.RepoURL)
	}

	if _, err := svc.VerifyProject(context.Background(), foreign.ID, p.ID); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("foreign VerifyProject err = %v, want ErrNotFound", err)
	}
}
