package action

import "time"

type AttemptState string

const (
	AttemptAuthorized    AttemptState = "authorized"
	AttemptDispatched    AttemptState = "dispatched"
	AttemptAcknowledged  AttemptState = "acknowledged"
	AttemptVerified      AttemptState = "verified"
	AttemptFailed        AttemptState = "failed"
	AttemptTimedOut      AttemptState = "timed_out"
	AttemptCancelled     AttemptState = "cancelled"
	AttemptIndeterminate AttemptState = "indeterminate"
)

type PostconditionState string

const (
	PostconditionPending PostconditionState = "pending"
	PostconditionPassed  PostconditionState = "passed"
	PostconditionFailed  PostconditionState = "failed"
	PostconditionUnknown PostconditionState = "unknown"
)

type CleanupState string

const (
	CleanupPending   CleanupState = "pending"
	CleanupSucceeded CleanupState = "succeeded"
	CleanupFailed    CleanupState = "failed"
)

type Outcome string

const (
	OutcomePending       Outcome = "pending"
	OutcomeVerified      Outcome = "verified"
	OutcomeFailed        Outcome = "failed"
	OutcomeCancelled     Outcome = "cancelled"
	OutcomeTimedOut      Outcome = "timed_out"
	OutcomeIndeterminate Outcome = "indeterminate"
)

type Attempt struct {
	ID                string
	Workspace         string
	DeviceID          string
	LeaseID           string
	Kind              Kind
	InvocationSurface InvocationSurface
	State             AttemptState
	IdempotencyKey    string
	RequestHash       string
	FencingToken      uint64
	ObservationToken  string
	Postcondition     PostconditionState
	Cleanup           CleanupState
	FailureClass      string
	CreatedAt         time.Time
	DispatchedAt      *time.Time
	FinishedAt        *time.Time
}

type Result struct {
	Attempt             Attempt
	Outcome             Outcome
	FailureClass        string
	PostconditionPassed bool
	CleanupSucceeded    bool
	IdempotentReplay    bool
}

type Completion struct {
	Workspace        string
	AttemptID        string
	DeviceID         string
	LeaseID          string
	HolderID         string
	FencingToken     uint64
	Postcondition    PostconditionState
	FailureClass     string
	ObservationToken string
}

type Reconciliation struct {
	Workspace        string
	AttemptID        string
	DeviceID         string
	LeaseID          string
	HolderID         string
	FencingToken     uint64
	ObservationToken string
	Succeeded        bool
}
