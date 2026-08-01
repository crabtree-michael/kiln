package agent

// Test-only exports (compiled solely into the package's test binary): they let the
// external agent_test package build the exact generational names the internal
// scheme produces, without leaking unexported helpers into the shipping API.

// SlotFragmentForTest exposes slotFragment so external tests can construct a slot's
// gen≥1 short name (the DNS-label-safe fragment form).
func SlotFragmentForTest(workerID string) string { return slotFragment(workerID) }

// MaxDNSLabelLenForTest exposes the DNS label limit the generational name must fit.
const MaxDNSLabelLenForTest = maxDNSLabelLen
