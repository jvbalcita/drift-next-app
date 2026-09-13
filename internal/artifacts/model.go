// Package artifacts owns artifact metadata and retention lifecycle. The byte
// store is an infrastructure boundary and is intentionally not here.
package artifacts

import "drift.local/drift-next/internal/domain"

type ArtifactID string

type State string

const (
	Pending             State = "pending"
	Stored              State = "stored"
	Referenced          State = "referenced"
	Retained            State = "retained"
	EligibleForDeletion State = "eligible_for_deletion"
	Deleted             State = "deleted"
	CleanupFailed       State = "cleanup_failed"
)

type RetentionClass string

const (
	RetentionCurrent            RetentionClass = "current"
	RetentionOperationalHistory RetentionClass = "operational_history"
	RetentionExecutionEvidence  RetentionClass = "execution_evidence"
	RetentionAuditSecurity      RetentionClass = "audit_security"
	RetentionDisposable         RetentionClass = "disposable"
)

func (s State) Valid() bool {
	switch s {
	case Pending, Stored, Referenced, Retained, EligibleForDeletion, Deleted, CleanupFailed:
		return true
	default:
		return false
	}
}

func CanTransition(from, to State) bool {
	switch from {
	case Pending:
		return to == Stored || to == CleanupFailed
	case Stored:
		return to == Referenced || to == Retained || to == EligibleForDeletion
	case Referenced:
		return to == Retained || to == EligibleForDeletion
	case Retained:
		return to == EligibleForDeletion
	case EligibleForDeletion:
		return to == Deleted || to == CleanupFailed
	case CleanupFailed:
		return to == EligibleForDeletion
	case Deleted:
		return false
	default:
		return false
	}
}

func Transition(from, to State) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("artifact", string(from), string(to))
	}
	return nil
}
