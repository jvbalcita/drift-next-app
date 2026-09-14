// Package artifacts owns artifact metadata and retention lifecycle. The byte
// store is an infrastructure boundary and is intentionally not here.
package artifacts

import (
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
)

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

type Artifact struct {
	ID             ArtifactID
	Workspace      organizations.WorkspaceID
	ContentHash    string
	SizeBytes      int64
	MediaType      string
	SchemaVersion  int
	RetentionClass RetentionClass
	State          State
	CreatedAt      time.Time
	DeletedAt      *time.Time
}

type Reference struct {
	ID         string
	Workspace  organizations.WorkspaceID
	ArtifactID ArtifactID
	OwnerType  string
	OwnerID    string
	State      string
	CreatedAt  time.Time
	EndedAt    *time.Time
}

const (
	ReferenceActive = "active"
	ReferenceEnded  = "ended"
)

func (r RetentionClass) Valid() bool {
	switch r {
	case RetentionCurrent, RetentionOperationalHistory, RetentionExecutionEvidence, RetentionAuditSecurity, RetentionDisposable:
		return true
	default:
		return false
	}
}

func (a Artifact) Validate() error {
	if strings.TrimSpace(string(a.ID)) == "" || strings.TrimSpace(string(a.Workspace)) == "" || strings.TrimSpace(a.ContentHash) == "" || strings.TrimSpace(a.MediaType) == "" || a.SizeBytes < 0 || a.SchemaVersion <= 0 || !a.RetentionClass.Valid() || !a.State.Valid() || a.CreatedAt.IsZero() {
		return fmt.Errorf("artifact metadata is invalid")
	}
	if len(a.ContentHash) > 256 || len(a.MediaType) > 256 {
		return fmt.Errorf("artifact metadata is unbounded")
	}
	if a.State == Deleted && a.DeletedAt == nil {
		return fmt.Errorf("deleted artifacts require a deletion time")
	}
	if a.DeletedAt != nil && a.DeletedAt.Before(a.CreatedAt) {
		return fmt.Errorf("artifact deletion time precedes creation")
	}
	return nil
}

func (r Reference) Validate() error {
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(string(r.Workspace)) == "" || strings.TrimSpace(string(r.ArtifactID)) == "" || strings.TrimSpace(r.OwnerType) == "" || strings.TrimSpace(r.OwnerID) == "" || r.CreatedAt.IsZero() {
		return fmt.Errorf("artifact reference identity is required")
	}
	if len(r.ID) > 256 || len(r.OwnerType) > 128 || len(r.OwnerID) > 256 {
		return fmt.Errorf("artifact reference is unbounded")
	}
	switch r.State {
	case ReferenceActive:
		if r.EndedAt != nil {
			return fmt.Errorf("active artifact references cannot have an end time")
		}
	case ReferenceEnded:
		if r.EndedAt == nil || r.EndedAt.Before(r.CreatedAt) {
			return fmt.Errorf("ended artifact references require an ordered end time")
		}
	default:
		return fmt.Errorf("artifact reference state is invalid")
	}
	return nil
}

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
