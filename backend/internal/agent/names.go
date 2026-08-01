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
//	gen ≥1 → "<prefix><slotUUID>-g<gen>"   — a fresh name past a squatting lower
//	         generation.
//
// parseWorkerName inverts both. A UUID's characters are hex (0-9a-f) plus
// hyphens, so the letter 'g' never occurs inside one and a "-g<n>" marker is
// unambiguous after a real slot UUID; a non-UUID remainder keeps its whole self
// as a gen-0 slot id, preserving adoption of any prefix-scoped worker the system
// named before generations existed.

// workerName derives the provider-side name for a slot at a given generation
// (05 §4, D5, 11 §3). gen ≤ 0 is the legacy bare name.
func workerName(prefix, workerID string, gen int) string {
	if gen <= 0 {
		return prefix + workerID
	}
	return fmt.Sprintf("%s%s-g%d", prefix, workerID, gen)
}

// parseWorkerName strips prefix and splits off a trailing "-g<gen>" generation
// marker when the part before it is a real slot UUID; otherwise the whole
// remainder is a gen-0 slot id. A name under a different prefix returns ok=false
// (not ours — the orphan/foreign case). Every prefix-scoped name therefore parses
// to the same slot id it was built from, so adoption, the turn machine, and the
// stale/orphan sweep all agree on which slot a sandbox belongs to. The returns are
// (slotID, generation, ok).
func parseWorkerName(prefix, name string) (string, int, bool) {
	rest, matched := strings.CutPrefix(name, prefix)
	if !matched || rest == "" {
		return "", 0, false
	}
	if i := strings.LastIndex(rest, "-g"); i > 0 {
		base, digits := rest[:i], rest[i+len("-g"):]
		if isSlotUUID(base) && isPositiveInt(digits) {
			n, err := strconv.Atoi(digits)
			if err == nil {
				return base, n, true
			}
		}
	}
	return rest, 0, true
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
