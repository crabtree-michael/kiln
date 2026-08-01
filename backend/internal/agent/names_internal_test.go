package agent

// Unit tests for the generational worker-name scheme (05 §4): gen 0 is the
// byte-identical legacy name so a deploy adopts the existing pool, gen ≥1 carries
// a "-g<n>" suffix, and parse inverts both without mis-reading a UUID or a
// non-UUID slot id. package agent (internal) so the unexported helpers are visible.

import (
	"fmt"
	"testing"
)

const (
	namePrefix = "kiln-worker-"
	nameUUID   = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
)

func TestWorkerNameGenerationZeroIsLegacyBareName(t *testing.T) {
	got := workerName(namePrefix, nameUUID, 0)
	if want := namePrefix + nameUUID; got != want {
		t.Fatalf("gen 0 name = %q, want the byte-identical legacy name %q (back-compat)", got, want)
	}
	// The exported default-prefix helper must agree with gen 0 under the default prefix.
	if WorkerName(nameUUID) != workerName(WorkerNamePrefix, nameUUID, 0) {
		t.Errorf("WorkerName(%q) must equal the gen-0 name under the default prefix", nameUUID)
	}
}

func TestWorkerNameGenerationSuffix(t *testing.T) {
	for _, gen := range []int{1, 2, 9, 42} {
		got := workerName(namePrefix, nameUUID, gen)
		if want := fmt.Sprintf("%s%s-g%d", namePrefix, nameUUID, gen); got != want {
			t.Errorf("gen %d name = %q, want %q", gen, got, want)
		}
	}
}

func TestParseWorkerNameRoundTrip(t *testing.T) {
	for _, gen := range []int{0, 1, 2, 37} {
		name := workerName(namePrefix, nameUUID, gen)
		uuid, g, ok := parseWorkerName(namePrefix, name)
		if !ok || uuid != nameUUID || g != gen {
			t.Errorf("parse(%q) = (%q, %d, %v), want (%q, %d, true)", name, uuid, g, ok, nameUUID, gen)
		}
	}
}

func TestParseWorkerNameRejectsForeignPrefix(t *testing.T) {
	if _, _, ok := parseWorkerName(namePrefix, "kiln-other-worker-"+nameUUID); ok {
		t.Error("a name under a different prefix is not ours — parse must return ok=false")
	}
	if _, _, ok := parseWorkerName(namePrefix, namePrefix); ok {
		t.Error("the bare prefix with no slot id is not a worker name")
	}
}

func TestParseWorkerNameDoesNotMisparseUUIDWithoutGeneration(t *testing.T) {
	// A UUID contains no 'g', so a bare UUID must be gen 0, never split on a phantom
	// "-g" marker.
	uuid, g, ok := parseWorkerName(namePrefix, namePrefix+nameUUID)
	if !ok || uuid != nameUUID || g != 0 {
		t.Errorf("parse(bare UUID) = (%q, %d, %v), want (%q, 0, true)", uuid, g, ok, nameUUID)
	}
}

func TestParseWorkerNameNonUUIDSlotStaysGenZero(t *testing.T) {
	// A non-UUID slot id keeps its whole remainder as a gen-0 id; the "-g" split only
	// applies after a real UUID, so a non-UUID base never gets a spurious generation.
	for _, id := range []string{"worker-1", "not-a-uuid-g5", nameUUID + "-g0"} {
		uuid, g, ok := parseWorkerName(namePrefix, namePrefix+id)
		if !ok || uuid != id || g != 0 {
			t.Errorf("parse(%q) = (%q, %d, %v), want (%q, 0, true)", namePrefix+id, uuid, g, ok, id)
		}
	}
}

func TestIsSlotUUID(t *testing.T) {
	if !isSlotUUID(nameUUID) {
		t.Errorf("%q should be a valid slot UUID", nameUUID)
	}
	for _, bad := range []string{"", "worker-1", nameUUID + "x", "aaaaaaaaXaaaa-aaaa-aaaa-aaaaaaaaaaaa"} {
		if isSlotUUID(bad) {
			t.Errorf("%q should NOT be a valid slot UUID", bad)
		}
	}
}
