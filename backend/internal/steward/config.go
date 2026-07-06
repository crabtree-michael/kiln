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

// Config parameterizes the sweep. Zero values fall back to the defaults, so the
// composition root can pass an unset (env-absent) value through untouched.
type Config struct {
	// Stall is the idle/stopped duration before the first poke, and (measured
	// from the poke) the grace before a re-stall escalates to Blocked.
	Stall time.Duration
	// Interval is the delay between sweeps.
	Interval time.Duration
}

func (c Config) withDefaults() Config {
	if c.Stall <= 0 {
		c.Stall = DefaultStall
	}
	if c.Interval <= 0 {
		c.Interval = DefaultInterval
	}
	return c
}
