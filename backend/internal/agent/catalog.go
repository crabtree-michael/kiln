package agent

import (
	"context"
	"time"
)

// SnapshotState is the provider-neutral capture lifecycle of a Snapshot. An
// adapter classifies its platform's own states into these; a state it does not
// recognise falls through to SnapshotUnknown so the dashboard can render it
// without ever naming a provider.
type SnapshotState string

const (
	// SnapshotReady is a fully-captured snapshot a project's workers can start
	// from — the only state safe to select as the project's base image.
	SnapshotReady SnapshotState = "ready"
	// SnapshotCapturing is a snapshot whose (slow) capture is still running; it
	// is listed so the user sees the capture in progress, but not yet selectable.
	SnapshotCapturing SnapshotState = "capturing"
	// SnapshotFailed is a snapshot whose capture failed terminally.
	SnapshotFailed SnapshotState = "failed"
	// SnapshotUnknown is the defensive default for an unrecognised provider state.
	SnapshotUnknown SnapshotState = "unknown"
)

// Snapshot is a provider-neutral saved workspace image a project can start its
// workers from (5 §6 Snapshots capability). Ref is the opaque handle stored as
// the project's snapshot selection and handed back to the provider at
// worker-create time (for Amika: the snapshot's fully-qualified name — the same
// value the project's AmikaSnapshot already carries); Name/Description are human
// labels; Source is the dev-box the snapshot was captured from, when known;
// State is the neutral capture lifecycle; CreatedAt is when it was captured. No
// provider vocabulary crosses this seam.
type Snapshot struct {
	Ref         string
	Name        string
	Description string
	Source      string
	State       SnapshotState
	CreatedAt   time.Time
}

// DevBox is a provider-neutral running workspace a snapshot can be captured
// from — an Amika "sandbox" the user develops in interactively, distinct from a
// pooled Kiln worker. Ref is the opaque handle SaveSnapshot captures from; Name
// is the human label; Status is its neutral run state.
type DevBox struct {
	Ref    string
	Name   string
	Status RunStatus
}

// SaveSnapshotRequest asks a provider to capture DevBoxRef as a new named
// snapshot (provider-neutral). Name is the human name for the new snapshot;
// Description is an optional note.
type SaveSnapshotRequest struct {
	DevBoxRef   string
	Name        string
	Description string
}

// SandboxCatalog is optionally implemented by a Provider whose Capabilities
// report a managed sandbox with snapshots (ManagedSandbox+Snapshots). It exposes
// the provider's snapshot catalog and dev-box capture so the dashboard can let a
// user pick which snapshot the project's workers start from and save a running
// dev box as a new snapshot — without the core naming a provider (05 abstraction
// rule). Read via SandboxCatalogOf; a provider that omits it offers no catalog,
// and the dashboard hides the affordance. Everything provider-specific stays
// inside the adapter — only the neutral Snapshot/DevBox shapes cross.
type SandboxCatalog interface {
	// ListSnapshots returns the snapshots the project may start its workers from,
	// newest first when the provider orders them.
	ListSnapshots(ctx context.Context) ([]Snapshot, error)
	// ListDevBoxes returns the running dev boxes a snapshot can be captured from.
	ListDevBoxes(ctx context.Context) ([]DevBox, error)
	// SaveSnapshot captures req.DevBoxRef as a new snapshot and returns it; the
	// capture may still be running (SnapshotCapturing) when it returns.
	SaveSnapshot(ctx context.Context, req SaveSnapshotRequest) (Snapshot, error)
}

// SandboxCatalogOf is the core's leak-free read of a provider's snapshot catalog
// (mirrors CapabilitiesOf): a provider that implements SandboxCatalog is
// returned with ok=true; one that does not gets (nil, false) and the caller
// reports the catalog unavailable rather than type-switching on a concrete
// adapter or naming a provider.
func SandboxCatalogOf(p Provider) (SandboxCatalog, bool) {
	c, ok := p.(SandboxCatalog)
	return c, ok
}
