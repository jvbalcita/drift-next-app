package action

import "fmt"

// ValidateSemanticTarget preserves ambiguity instead of choosing a fuzzy
// match. The target contains stable semantic hints only; coordinate fallback
// is a later adapter decision and cannot be smuggled through this contract.
func ValidateSemanticTarget(target SemanticTarget, candidateCount int, actionable, enabled bool) error {
	if target.Empty() {
		return fmt.Errorf("semantic target is required")
	}
	if candidateCount < 1 {
		return fmt.Errorf("target was not found")
	}
	if candidateCount > 1 {
		return fmt.Errorf("target is ambiguous")
	}
	if !actionable || !enabled {
		return fmt.Errorf("target is not actionable")
	}
	return nil
}
