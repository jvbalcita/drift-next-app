// Package clock defines the time seam used by application services and tests.
package clock

import "time"

// Clock returns the current time for a caller. Implementations must return UTC
// values so persisted timestamps have one representation.
type Clock interface {
	Now() time.Time
}

// System is the production clock backed by the system wall clock.
type System struct{}

// Now returns the current UTC time.
func (System) Now() time.Time {
	return time.Now().UTC()
}

// Fixed is a deterministic clock for tests and other controlled callers.
type Fixed struct {
	at time.Time
}

// NewFixed returns a clock fixed at at. The instant is normalized to UTC.
func NewFixed(at time.Time) Fixed {
	return Fixed{at: at.UTC()}
}

// Now returns the fixed UTC instant.
func (f Fixed) Now() time.Time {
	return f.at
}
