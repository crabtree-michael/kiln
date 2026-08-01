package agent

import (
	"fmt"
	"strconv"
	"strings"
)

// The provider-side name for a board slot's sandbox carries a stateless
// GENERATION suffix so a failed/partial provision that leaves an orphaned VM
// squatting the deterministic name (auto_delete off — 05 D6) never wedges the
// slot: the reconciler simply rebuilds the slot at the next generation under a
// fresh name (the amika-sandbox-name-conflict prod fix). The generation is
// DERIVED from the provider's own live list each sweep — no board column and no
// new persistence; the adapter stays stateless and the board keeps owning its
// workers table (05 §4).
//
//	gen 0  → "<prefix><slotUUID>"          — the legacy bare name, byte-identical
//	         to the pre-generation scheme so a deploy adopts the existing pool
//	         without recreating it (back-compat is the whole point of gen 0).
//	gen ≥1 → "<prefix><frag>-g<gen>"       — a fresh name past a squatting lower
//	         generation, where frag = slotFragment(slotUUID) (the first 12 hex
//	         chars of the UUID with dashes removed).
//
// The gen≥1 name uses a SHORT fragment, not the full UUID, for a load-bearing
// reason: prod's per-project prefix already runs ~26 chars (e.g.
// "kiln-prod-worker-043f381f-"), so "<prefix><fullUUID>-g<gen>" reaches ~65 chars
// and BLOWS the 63-char DNS label limit — Amika 400s every such name, so the
// rotation never healed and the slot stayed wedged (the amika-sandbox-name-conflict
// prod bug). The fragment keeps gen≥1 names ~43 chars, comfortably under 63.
//
// parseWorkerName inverts both. A UUID's characters are hex (0-9a-f) plus
// hyphens, so the letter 'g' never occurs inside one and a "-g<n>" marker is
// unambiguous after a real slot UUID or its hex fragment; a non-matching remainder
// keeps its whole self as a gen-0 slot id, preserving adoption of any prefix-scoped
// worker the system named before generations existed. Because a gen≥1 remainder is
// the fragment (not the full UUID), name→slot grouping is board-slot-driven:
// slotMatches(id, remainder) tests the remainder against both the full id and
// slotFragment(id).

// maxDNSLabelLen is the 63-char limit on a single DNS label. A provider worker
// name is one label (no dots), so it must fit — the invariant the gen≥1 fragment
// exists to guarantee. The names_internal regression test enforces it for a
// realistic prod prefix and gen 1..999 (the shipped-bug guard).
const maxDNSLabelLen = 63

// slotFragmentLen is how many leading hex characters of a dash-stripped slot UUID
// seed a gen≥1 name. 12 hex = 48 bits: for a project's handful of slots the chance
// two share a fragment is negligible, and 12+len("<prefix>-g<gen>") stays well under
// maxDNSLabelLen for realistic prefixes.
const slotFragmentLen = 12

// workerName derives the provider-side name for a slot at a given generation
// (05 §4, D5, 11 §3). gen ≤ 0 is the legacy bare name (full UUID, byte-identical
// to the pre-generation scheme); gen ≥1 uses the short DNS-label-safe fragment.
func workerName(prefix, workerID string, gen int) string {
	if gen <= 0 {
		return prefix + workerID
	}
	return fmt.Sprintf("%s%s-g%d", prefix, slotFragment(workerID), gen)
}

// slotFragment is the lowercase-hex identifier a gen≥1 name carries for a slot:
// the first slotFragmentLen non-dash characters of workerID, lowercased so the
// result is a DNS-valid label component. For a standard UUID this is its first 12
// hex digits (dashes stripped), e.g.
// "3cddd784-b432-43e3-8489-b0d3625fc787" → "3cddd784b432".
func slotFragment(workerID string) string {
	frag := make([]byte, 0, slotFragmentLen)
	for i := 0; i < len(workerID) && len(frag) < slotFragmentLen; i++ {
		c := workerID[i]
		if c == '-' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		frag = append(frag, c)
	}
	return string(frag)
}

// slotMatches reports whether a parsed name remainder belongs to board slot id:
// either the full-UUID form (gen 0, or a legacy full-UUID gen≥1) or the short
// fragment form (gen≥1). This is the board-slot-driven grouping the short gen≥1
// name requires — the remainder alone no longer reconstructs the full UUID.
func slotMatches(slotID, remainder string) bool {
	return remainder == slotID || remainder == slotFragment(slotID)
}

// parseWorkerName strips prefix and splits off a trailing "-g<gen>" generation
// marker when the part before it is a real slot UUID or a slot fragment; otherwise
// the whole remainder is a gen-0 slot id. A name under a different prefix returns
// ok=false (not ours — the orphan/foreign case). The returns are
// (slotIDOrFragment, generation, ok): for gen 0 the remainder is the full slot id;
// for gen≥1 it is slotFragment(id), so callers resolve the owning slot via
// slotMatches, not exact equality.
func parseWorkerName(prefix, name string) (string, int, bool) {
	rest, matched := strings.CutPrefix(name, prefix)
	if !matched || rest == "" {
		return "", 0, false
	}
	if i := strings.LastIndex(rest, "-g"); i > 0 {
		base, digits := rest[:i], rest[i+len("-g"):]
		if (isSlotUUID(base) || isSlotFragment(base)) && isPositiveInt(digits) {
			n, err := strconv.Atoi(digits)
			if err == nil {
				return base, n, true
			}
		}
	}
	return rest, 0, true
}

// isSlotFragment reports whether s is exactly a slotFragment: slotFragmentLen
// lowercase hex digits. Only such a base lets a trailing "-g<n>" be read as a
// generation on the short name form, so a non-fragment remainder is never
// mis-parsed.
func isSlotFragment(s string) bool {
	if len(s) != slotFragmentLen {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// slotUUIDLen is the length of a standard 8-4-4-4-12 hex UUID.
const slotUUIDLen = 36

// isSlotUUID reports whether s is a standard 36-char UUID (8-4-4-4-12 hex). Only a
// real UUID base lets a trailing "-g<n>" be read as a generation, so a non-UUID
// slot id (e.g. a test worker id) is never mis-parsed.
func isSlotUUID(s string) bool {
	if len(s) != slotUUIDLen {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !isHexDigit(c) {
			return false
		}
	}
	return true
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// isPositiveInt reports whether s is a run of digits denoting an integer ≥ 1 — a
// canonical generation suffix. gen 0 is the bare name, so "-g0" is not a
// generation and stays part of the slot id.
func isPositiveInt(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1
}
