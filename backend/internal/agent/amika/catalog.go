package amika

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
)

// SaveSnapshot input guards (checked before any request is made).
var (
	errSnapshotNameRequired   = errors.New("amika: save snapshot: name is required")
	errSnapshotDevBoxRequired = errors.New("amika: save snapshot: dev box ref is required")
)

// This file implements agent.SandboxCatalog (catalog.go) over v0beta1's
// SandboxSnapshots + Sandboxes endpoints — the snapshot picker and dev-box
// capture the dashboard drives:
//
//	ListSnapshots — GET /sandbox-snapshots: the base images a project's workers
//	                can start from. The neutral Snapshot.Ref is the snapshot's
//	                `snapshot` fully-qualified name (the create-sandbox `snapshot`
//	                field — the same value the project's AmikaSnapshot stores),
//	                NOT its id.
//	ListDevBoxes  — GET /sandboxes, filtered to sandboxes this instance does NOT
//	                own (no worker prefix): the user's interactive dev boxes, the
//	                complement of ListWorkers, so a pooled Kiln worker is never
//	                offered as a capture source.
//	SaveSnapshot  — POST /sandbox-snapshots: capture a dev box as a new snapshot
//	                (Amika default mode scrub_and_delete — strip injected secrets,
//	                then delete the source), which then appears in ListSnapshots.
//
// Snapshot/sandbox `state` is un-enumerated in v0beta1, so classification is
// defensive (states.go) — the one place to harden as real values are observed.

var _ agent.SandboxCatalog = (*Client)(nil)

// ListSnapshots returns the account's sandbox snapshots as neutral
// agent.Snapshots. Ref is the fully-qualified `snapshot` name a project stores
// and passes back at worker-create time.
func (c *Client) ListSnapshots(ctx context.Context) ([]agent.Snapshot, error) {
	var resp snapshotList
	if err := c.do(ctx, http.MethodGet, "/sandbox-snapshots", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]agent.Snapshot, 0, len(resp.Items))
	for _, s := range resp.Items {
		out = append(out, snapshotToNeutral(s))
	}
	return out, nil
}

// ListDevBoxes returns the running sandboxes that are NOT this instance's pooled
// workers — the user's interactive dev boxes, the complement of ListWorkers's
// prefix match — so a Kiln worker is never offered as a capture source.
func (c *Client) ListDevBoxes(ctx context.Context) ([]agent.DevBox, error) {
	var list []sandbox
	if err := c.do(ctx, http.MethodGet, "/sandboxes", nil, &list); err != nil {
		return nil, err
	}
	out := make([]agent.DevBox, 0, len(list))
	for _, s := range list {
		if strings.HasPrefix(s.Name, c.cfg.WorkerPrefix) {
			continue // a pooled Kiln worker, not a dev box
		}
		out = append(out, agent.DevBox{
			Ref:    firstNonEmpty(s.ID, s.Name),
			Name:   s.Name,
			Status: runStatus(classifyState(s.State)),
		})
	}
	return out, nil
}

// SaveSnapshot captures req.DevBoxRef as a new named snapshot. The capture is
// async (Amika records it in the `capturing` state and runs the slow Daytona
// capture in the background), so the returned Snapshot may still be capturing —
// it becomes selectable once ListSnapshots reports it ready.
func (c *Client) SaveSnapshot(ctx context.Context, req agent.SaveSnapshotRequest) (agent.Snapshot, error) {
	if strings.TrimSpace(req.Name) == "" {
		return agent.Snapshot{}, errSnapshotNameRequired
	}
	if strings.TrimSpace(req.DevBoxRef) == "" {
		return agent.Snapshot{}, errSnapshotDevBoxRequired
	}
	body := createSnapshotRequest{
		Name:        req.Name,
		SandboxID:   req.DevBoxRef,
		Description: req.Description,
	}
	var s snapshotObject
	if err := c.do(ctx, http.MethodPost, "/sandbox-snapshots", body, &s); err != nil {
		return agent.Snapshot{}, err
	}
	return snapshotToNeutral(s), nil
}

// snapshotToNeutral maps one Amika snapshot object onto the neutral
// agent.Snapshot: Ref is the fully-qualified `snapshot` name (falling back to
// the id until the name is assigned), and the state is classified defensively.
func snapshotToNeutral(s snapshotObject) agent.Snapshot {
	at, _ := time.Parse(time.RFC3339, s.CreatedAt) //nolint:errcheck // best-effort; zero At on failure
	return agent.Snapshot{
		Ref:         firstNonEmpty(s.Snapshot, s.ID),
		Name:        firstNonEmpty(s.Snapshot, s.ID),
		Description: s.Description,
		Source:      s.SourceSandboxName,
		State:       classifySnapshotState(s.State),
		CreatedAt:   at,
	}
}
