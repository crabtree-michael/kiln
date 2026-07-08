package steward

import "time"

// Default sweep parameters. The stall threshold sits at the low end of the
// 5–10 minute band the design chose: long enough that a routine pause between
// turns never trips it, short enough that a genuine hang is surfaced promptly.
// The interval is how often the sweep runs — well under the threshold so a
// stall is caught within roughly one interval of crossing it.
const (
	DefaultStall    = 5 * time.Minute
	DefaultInterval = time.Minute
)

// Backoff bounds for retrying a poke whose delivery to the agent failed. A
// delivery failure leaves nothing recorded, so without a cooldown the next
// sweep would re-attempt immediately and keep hammering a transiently unhealthy
// agent/board every interval. Instead the retry delay grows geometrically from
// BackoffBase, doubling per consecutive failure up to BackoffCap.
const (
	DefaultBackoffBase = time.Minute
	DefaultBackoffCap  = 15 * time.Minute
)

// Config parameterizes the sweep. Zero values fall back to the defaults, so the
// composition root can pass an unset (env-absent) value through untouched.
type Config struct {
	// Stall is the idle/stopped duration before the first poke, and (measured
	// from the poke) the grace before a re-stall escalates to Blocked.
	Stall time.Duration
	// Interval is the delay between sweeps.
	Interval time.Duration
	// BackoffBase is the cooldown after the first failed poke delivery; it
	// doubles per consecutive failure, capped at BackoffCap.
	BackoffBase time.Duration
	// BackoffCap is the ceiling on the poke-retry cooldown.
	BackoffCap time.Duration
}

func (c Config) withDefaults() Config {
	if c.Stall <= 0 {
		c.Stall = DefaultStall
	}
	if c.Interval <= 0 {
		c.Interval = DefaultInterval
	}
	if c.BackoffBase <= 0 {
		c.BackoffBase = DefaultBackoffBase
	}
	if c.BackoffCap <= 0 {
		c.BackoffCap = DefaultBackoffCap
	}
	if c.BackoffCap < c.BackoffBase {
		c.BackoffCap = c.BackoffBase
	}
	return c
}
