package identity

import "time"

// SetClockForTest overrides the service clock. Test-only: this file compiles
// only under `go test`, so the production binary ships no clock setter.
func SetClockForTest(s *Service, now func() time.Time) { s.now = now }
