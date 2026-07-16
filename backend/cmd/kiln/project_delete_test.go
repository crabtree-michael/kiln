package main

// Unit tests for projectDeleteCoordinator (12 §5): it authorizes the caller as
// the project's owner FIRST (so the destructive cascade never runs for a project
// the caller can't delete), then purges state, tears down workers, evicts the
// tenant bundle, removes the on-disk clone, and soft-deletes the row. This pins
// the ordering, the authorize-gate, clone removal, and error propagation against
// fakes; the live cascade is proven in the api tenancy integration suite.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

const (
	delUserID    = "user-1"
	delProjectID = "proj-1"
)

var errFakeDeleteCascade = errors.New("synthetic state delete failure (delete)")

type fakeProjectAuthorizer struct {
	order        *[]string
	authErr      error
	deleteErr    error
	authUserID   string
	authProject  string
	delUserID    string
	delProjectID string
}

func (f *fakeProjectAuthorizer) ProjectByID(_ context.Context, userID, projectID string) (identity.Project, error) {
	f.authUserID, f.authProject = userID, projectID
	*f.order = append(*f.order, stepAuthorize)
	if f.authErr != nil {
		return identity.Project{}, f.authErr
	}
	return identity.Project{ID: projectID, OwnerUserID: userID}, nil
}

func (f *fakeProjectAuthorizer) SoftDeleteProject(_ context.Context, userID, projectID string) error {
	f.delUserID, f.delProjectID = userID, projectID
	*f.order = append(*f.order, stepSoftDelete)
	return f.deleteErr
}

type fakeEvictor struct {
	order     *[]string
	projectID string
}

func (f *fakeEvictor) Invalidate(projectID string) {
	f.projectID = projectID
	*f.order = append(*f.order, stepEvict)
}

// delHarness bundles the coordinator with the concrete fakes behind it, so a
// test reads the recorded calls off typed fields (no dogsled, no assertions).
type delHarness struct {
	c       *projectDeleteCoordinator
	auth    *fakeProjectAuthorizer
	evictor *fakeEvictor
	state   *fakeStateDeleter
	workers *fakeWorkerResetter
	order   *[]string
}

func newDeleteHarness(repoDir string) *delHarness {
	order := &[]string{}
	auth := &fakeProjectAuthorizer{order: order}
	evictor := &fakeEvictor{order: order}
	state := &fakeStateDeleter{order: order}
	workers := &fakeWorkerResetter{order: order}
	return &delHarness{
		c: &projectDeleteCoordinator{
			auth: auth, state: state, workers: workers, registry: evictor, repoDir: repoDir,
		},
		auth: auth, evictor: evictor, state: state, workers: workers, order: order,
	}
}

func TestProjectDelete_CascadesInOrder(t *testing.T) {
	h := newDeleteHarness("")

	if err := h.c.DeleteProject(context.Background(), delUserID, delProjectID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	want := []string{stepAuthorize, stepDelete, stepWorkers, stepEvict, stepSoftDelete}
	if got := *h.order; len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i, w := range want {
		if (*h.order)[i] != w {
			t.Fatalf("order = %v, want %v", *h.order, want)
		}
	}
	// Every step is scoped to the caller's user/project.
	if h.auth.authUserID != delUserID || h.auth.authProject != delProjectID {
		t.Errorf("authorize saw (%s,%s), want (%s,%s)", h.auth.authUserID, h.auth.authProject, delUserID, delProjectID)
	}
	if h.state.projectID != delProjectID || h.workers.projectID != delProjectID || h.evictor.projectID != delProjectID {
		t.Errorf("a step ran for the wrong project: state=%s workers=%s evict=%s",
			h.state.projectID, h.workers.projectID, h.evictor.projectID)
	}
	if h.auth.delUserID != delUserID || h.auth.delProjectID != delProjectID {
		t.Errorf("soft-delete saw (%s,%s), want (%s,%s)",
			h.auth.delUserID, h.auth.delProjectID, delUserID, delProjectID)
	}
}

func TestProjectDelete_NotOwned_SkipsCascade(t *testing.T) {
	h := newDeleteHarness("")
	h.auth.authErr = identity.ErrNotFound

	err := h.c.DeleteProject(context.Background(), delUserID, delProjectID)
	if !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("DeleteProject err = %v, want ErrNotFound", err)
	}
	if h.state.calls != 0 || h.workers.calls != 0 {
		t.Errorf("cascade ran despite failed authorize: state=%d workers=%d", h.state.calls, h.workers.calls)
	}
	if got := *h.order; len(got) != 1 || got[0] != stepAuthorize {
		t.Errorf("order = %v, want only [authorize]", got)
	}
}

func TestProjectDelete_RemovesClone(t *testing.T) {
	dir := t.TempDir()
	cloneDir := filepath.Join(dir, delProjectID)
	if err := os.MkdirAll(cloneDir, 0o750); err != nil {
		t.Fatalf("seed clone dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cloneDir, "HEAD"), []byte("ref: main"), 0o600); err != nil {
		t.Fatalf("seed clone file: %v", err)
	}
	h := newDeleteHarness(dir)

	if err := h.c.DeleteProject(context.Background(), delUserID, delProjectID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := os.Stat(cloneDir); !os.IsNotExist(err) {
		t.Errorf("clone dir still exists after delete (stat err = %v)", err)
	}
}

func TestProjectDelete_StateError_SkipsSoftDelete(t *testing.T) {
	h := newDeleteHarness("")
	h.state.err = errFakeDeleteCascade

	if err := h.c.DeleteProject(context.Background(), delUserID, delProjectID); err == nil {
		t.Fatal("expected error when state delete fails")
	}
	if h.auth.delProjectID != "" {
		t.Errorf("soft-delete ran despite a state-delete failure (project %q)", h.auth.delProjectID)
	}
}
