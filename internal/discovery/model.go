// Package discovery owns non-authoritative scan runs and candidate approval.
package discovery

import (
	"encoding/json"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/organizations"
)

type ScanRunID string
type ScanCandidateID string
type ApprovalDecisionID string

type ScanRunState string

const (
	ScanRequested ScanRunState = "requested"
	ScanRunning   ScanRunState = "running"
	ScanCompleted ScanRunState = "completed"
	ScanFailed    ScanRunState = "failed"
	ScanCancelled ScanRunState = "cancelled"
)

type CandidateState string

const (
	CandidateDiscovered      CandidateState = "discovered"
	CandidatePendingApproval CandidateState = "pending_approval"
	CandidateApproved        CandidateState = "approved"
	CandidateRejected        CandidateState = "rejected"
	CandidateExpired         CandidateState = "expired"
	CandidateRegistered      CandidateState = "registered"
)

type Decision string

const (
	DecisionApproved Decision = "approved"
	DecisionRejected Decision = "rejected"
	DecisionExpired  Decision = "expired"
)

type ScanRun struct {
	ID               ScanRunID
	Workspace        organizations.WorkspaceID
	NetworkProfileID networkprofiles.NetworkProfileID
	State            ScanRunState
	RequestedAt      time.Time
	StartedAt        *time.Time
	CompletedAt      *time.Time
	IdempotencyKey   string
	FailureClass     domain.FailureClass
}

type ScanCandidate struct {
	ID           ScanCandidateID
	Workspace    organizations.WorkspaceID
	ScanRunID    ScanRunID
	CandidateKey string
	Host         string
	Port         uint16
	Serial       string
	Fingerprint  string
	State        CandidateState
	DiscoveredAt time.Time
	ExpiresAt    *time.Time
	EvidenceJSON string
}

type ApprovalDecision struct {
	ID          ApprovalDecisionID
	Workspace   organizations.WorkspaceID
	CandidateID ScanCandidateID
	Decision    Decision
	DecidedAt   time.Time
	ActorID     string
	Reason      string
}

type RegistrationEvent struct {
	ID          string
	Workspace   organizations.WorkspaceID
	CandidateID ScanCandidateID
	DeviceID    devices.DeviceID
	EndpointID  string
	Outcome     string
	ActorID     string
	OccurredAt  time.Time
	DetailsJSON string
}

type ObservedCandidate struct {
	CandidateKey string
	Host         string
	Port         uint16
	Serial       string
	Fingerprint  string
	Evidence     map[string]string
	ExpiresAt    *time.Time
}

func (c ObservedCandidate) Valid() bool {
	return c.CandidateKey != "" && (c.Host != "" || c.Serial != "") && c.Port > 0
}

func (c ObservedCandidate) EvidenceJSON() string {
	if len(c.Evidence) == 0 {
		return "{}"
	}
	data, err := json.Marshal(c.Evidence)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func (s ScanRunState) Valid() bool {
	switch s {
	case ScanRequested, ScanRunning, ScanCompleted, ScanFailed, ScanCancelled:
		return true
	default:
		return false
	}
}

func CanTransitionScanRun(from, to ScanRunState) bool {
	switch from {
	case ScanRequested:
		return to == ScanRunning || to == ScanCancelled
	case ScanRunning:
		return to == ScanCompleted || to == ScanFailed || to == ScanCancelled
	case ScanCompleted, ScanFailed, ScanCancelled:
		return false
	default:
		return false
	}
}

func TransitionScanRun(from, to ScanRunState) error {
	if !CanTransitionScanRun(from, to) {
		return domain.InvalidTransition("scan_run", string(from), string(to))
	}
	return nil
}

func (s CandidateState) Valid() bool {
	switch s {
	case CandidateDiscovered, CandidatePendingApproval, CandidateApproved,
		CandidateRejected, CandidateExpired, CandidateRegistered:
		return true
	default:
		return false
	}
}

func CanTransitionCandidate(from, to CandidateState) bool {
	switch from {
	case CandidateDiscovered:
		return to == CandidatePendingApproval || to == CandidateExpired
	case CandidatePendingApproval:
		return to == CandidateApproved || to == CandidateRejected || to == CandidateExpired
	case CandidateApproved:
		return to == CandidateRegistered || to == CandidateExpired
	case CandidateRejected, CandidateExpired, CandidateRegistered:
		return false
	default:
		return false
	}
}

func TransitionCandidate(from, to CandidateState) error {
	if !CanTransitionCandidate(from, to) {
		return domain.InvalidTransition("scan_candidate", string(from), string(to))
	}
	return nil
}

// CanRegister is intentionally narrower than a generic transition check: an
// approval decision is required before a candidate can create a device.
func CanRegister(state CandidateState) bool {
	return state == CandidateApproved
}
