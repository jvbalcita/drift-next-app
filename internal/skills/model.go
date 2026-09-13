// Package skills owns reviewed, immutable deterministic-skill promotion state.
package skills

import "drift.local/drift-next/internal/domain"

type SkillID string
type SkillVersionID string

type State string

const (
	Draft      State = "draft"
	Validated  State = "validated"
	Published  State = "published"
	Deprecated State = "deprecated"
	Retired    State = "retired"
)

type TrustState string

const (
	TrustUnreviewed TrustState = "unreviewed"
	TrustReviewed   TrustState = "reviewed"
	TrustApproved   TrustState = "approved"
	TrustRevoked    TrustState = "revoked"
)

func (s State) Valid() bool {
	switch s {
	case Draft, Validated, Published, Deprecated, Retired:
		return true
	default:
		return false
	}
}

func CanTransition(from, to State) bool {
	switch from {
	case Draft:
		return to == Validated || to == Retired
	case Validated:
		return to == Draft || to == Published || to == Retired
	case Published:
		return to == Deprecated || to == Retired
	case Deprecated:
		return to == Retired
	case Retired:
		return false
	default:
		return false
	}
}

func Transition(from, to State) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("skill", string(from), string(to))
	}
	return nil
}
