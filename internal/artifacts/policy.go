package artifacts

import "fmt"

// PolicyConfig is the validated quota and retention seam. Exact legal-hold
// durations and cleanup cadences remain unresolved policy decisions; callers
// supply them explicitly rather than inventing values here.
type PolicyConfig struct {
	// MaxObjectBytes bounds a single admitted object. Zero means DefaultMaxObjectBytes.
	MaxObjectBytes int64
	// MaxWorkspaceBytes bounds aggregate stored bytes for one workspace.
	// Zero means unlimited at this seam (operators must still set quotas later).
	MaxWorkspaceBytes int64
	// AllowDisposableCleanup enables cleanup of disposable-class artifacts that
	// are eligible_for_deletion. Other classes require an explicit retain→eligible
	// path and never auto-delete protected references.
	AllowDisposableCleanup bool
}

// DefaultMaxObjectBytes is a safe single-object ceiling (64 MiB).
const DefaultMaxObjectBytes int64 = 64 << 20

// DefaultPolicy returns conservative, fail-closed defaults without inventing
// retention durations or legal holds.
func DefaultPolicy() PolicyConfig {
	return PolicyConfig{
		MaxObjectBytes:         DefaultMaxObjectBytes,
		MaxWorkspaceBytes:      0,
		AllowDisposableCleanup: true,
	}
}

// Validate reports whether the policy values are usable.
func (p PolicyConfig) Validate() error {
	if p.MaxObjectBytes < 0 {
		return fmt.Errorf("max object bytes cannot be negative")
	}
	if p.MaxWorkspaceBytes < 0 {
		return fmt.Errorf("max workspace bytes cannot be negative")
	}
	return nil
}

// EffectiveMaxObjectBytes returns the configured or default object size limit.
func (p PolicyConfig) EffectiveMaxObjectBytes() int64 {
	if p.MaxObjectBytes == 0 {
		return DefaultMaxObjectBytes
	}
	return p.MaxObjectBytes
}
