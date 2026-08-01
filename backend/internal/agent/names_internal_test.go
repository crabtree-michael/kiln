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
	// gen ≥1 carries the SHORT slot fragment (not the full UUID) so the name stays a
	// DNS-label-safe length — the amika-sandbox-name-conflict prod fix.
	for _, gen := range []int{1, 2, 9, 42} {
		got := workerName(namePrefix, nameUUID, gen)
		if want := fmt.Sprintf("%s%s-g%d", namePrefix, slotFragment(nameUUID), gen); got != want {
			t.Errorf("gen %d name = %q, want %q", gen, got, want)
		}
	}
}

// TestWorkerNameGenerationIsDNSLabelSafe is the regression test for the shipped
// wedge. With a realistic prod prefix the OLD "<prefix><fullUUID>-g<gen>" scheme
// exceeded the 63-char DNS label limit so every rotation 400'd; the fragment scheme
// must keep every generation ≤ 63 and a valid single DNS label.
func TestWorkerNameGenerationIsDNSLabelSafe(t *testing.T) {
	// The exact prod shapes from the wedge logs: an ~26-char per-project prefix and a
	// full slot UUID.
	const prodPrefix = "kiln-prod-worker-043f381f-"
	const prodUUID = "3cddd784-b432-43e3-8489-b0d3625fc787"

	// Guard the guard: the OLD scheme genuinely blew the limit (26+36+3 = 65), so this
	// test actually protects against a regression rather than passing vacuously.
	oldGen1 := prodPrefix + prodUUID + "-g1"
	if len(oldGen1) <= maxDNSLabelLen {
		t.Fatalf("expected the OLD gen-1 name %q (%d chars) to EXCEED the %d-char limit; "+
			"the regression guard is miscalibrated", oldGen1, len(oldGen1), maxDNSLabelLen)
	}
	if len(oldGen1) != 65 {
		t.Errorf("prod OLD gen-1 length changed: got %d, expected 65 (26 prefix + 36 uuid + 3 \"-g1\")", len(oldGen1))
	}

	for _, gen := range []int{1, 2, 9, 42, 999} {
		name := workerName(prodPrefix, prodUUID, gen)
		if len(name) > maxDNSLabelLen {
			t.Errorf("gen %d name %q is %d chars, exceeds the %d-char DNS label limit",
				gen, name, len(name), maxDNSLabelLen)
		}
		if !isDNSLabel(name) {
			t.Errorf("gen %d name %q is not a valid single DNS label", gen, name)
		}
	}
	// Document the new length for the canonical prod example (26 + 12 + 3).
	if got, want := len(workerName(prodPrefix, prodUUID, 1)), 41; got != want {
		t.Errorf("prod gen-1 name length = %d, want %d", got, want)
	}
}

func TestSlotFragment(t *testing.T) {
	if got, want := slotFragment("3cddd784-b432-43e3-8489-b0d3625fc787"), "3cddd784b432"; got != want {
		t.Errorf("slotFragment = %q, want %q", got, want)
	}
	// Exactly slotFragmentLen lowercase hex chars, and a valid fragment.
	frag := slotFragment(nameUUID)
	if len(frag) != slotFragmentLen || !isSlotFragment(frag) {
		t.Errorf("slotFragment(%q) = %q, want %d lowercase-hex chars", nameUUID, frag, slotFragmentLen)
	}
	// Uppercase UUID digits are lowercased so the fragment is DNS-valid.
	if got := slotFragment("ABCDEF01-2345-6789-ABCD-EF0123456789"); got != "abcdef012345" {
		t.Errorf("slotFragment must lowercase hex, got %q", got)
	}
}

func TestParseWorkerNameRoundTrip(t *testing.T) {
	for _, gen := range []int{0, 1, 2, 37} {
		name := workerName(namePrefix, nameUUID, gen)
		rem, g, ok := parseWorkerName(namePrefix, name)
		if !ok || g != gen {
			t.Errorf("parse(%q) = (%q, %d, %v), want gen %d, ok", name, rem, g, ok, gen)
		}
		// The remainder is the full uuid at gen 0, the fragment at gen≥1 — either way
		// it must resolve to the originating slot via slotMatches.
		if !slotMatches(nameUUID, rem) {
			t.Errorf("parse(%q) remainder %q does not match slot %q", name, rem, nameUUID)
		}
	}
}

// A gen≥1 short name for one slot must NOT be grouped onto a different slot: the
// fragment identifies exactly its own slot.
func TestSlotMatchesRejectsOtherSlots(t *testing.T) {
	const other = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	name := workerName(namePrefix, nameUUID, 3)
	rem, _, ok := parseWorkerName(namePrefix, name)
	if !ok {
		t.Fatalf("parse(%q) failed", name)
	}
	if slotMatches(other, rem) {
		t.Errorf("gen-3 name for %q must not match slot %q", nameUUID, other)
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

// isDNSLabel reports whether name is a valid single DNS label (RFC 1123): 1–63
// chars, lowercase letters/digits/hyphens, not starting or ending with a hyphen.
// This is what Amika enforces on a sandbox name, so it is the shape the generational
// name must always satisfy.
func isDNSLabel(name string) bool {
	if name == "" || len(name) > maxDNSLabelLen {
		return false
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for i := range len(name) {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			continue
		}
		return false
	}
	return true
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
