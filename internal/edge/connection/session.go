package connection

import (
	"sync"
	"time"

	"drift.local/drift-next/internal/action"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// Runtime connection observations for an edge agent. These are never leases.
type RuntimeState string

const (
	RuntimeConnected    RuntimeState = "connected"
	RuntimeReconnecting RuntimeState = "reconnecting"
	RuntimeDisconnected RuntimeState = "disconnected"
)

type DispatchOutcome string

const (
	DispatchAccepted      DispatchOutcome = "accepted"
	DispatchIndeterminate DispatchOutcome = "indeterminate"
	DispatchResolved      DispatchOutcome = "resolved"
)

type ResolutionKind string

const (
	ResolutionFreshObservation  ResolutionKind = "fresh_observation"
	ResolutionOperatorConfirmed ResolutionKind = "operator_confirmed"
)

type SessionConfig struct {
	AgentID       string
	TransportID   string
	Protocol      string
	HelperToken   string
	PolicyVersion uint64
}

type Status struct {
	State                RuntimeState
	TransportID          string
	Protocol             string
	HelperAttached       bool
	HelperTokenRotatedAt *time.Time
	DisconnectedReason   string
	PolicyVersion        uint64
	PendingIndeterminate int
	HelperTokenIsLease   bool
	TransportIDIsLease   bool
	UpdatedAt            time.Time
}

type ReconnectEvidence struct {
	TransportID string
	Protocol    string
}

type Session struct {
	mu               sync.Mutex
	cfg              SessionConfig
	state            RuntimeState
	transportID      string
	protocol         string
	helperAttached   bool
	helperRotatedAt  *time.Time
	disconnectReason string
	policyVersion    uint64
	updatedAt        time.Time
	dispatches       map[string]DispatchOutcome
}

func NewSession(cfg SessionConfig, now time.Time) *Session {
	now = now.UTC()
	policy := cfg.PolicyVersion
	if policy == 0 {
		policy = 1
	}
	return &Session{
		cfg:            cfg,
		state:          RuntimeConnected,
		transportID:    cfg.TransportID,
		protocol:       cfg.Protocol,
		helperAttached: cfg.HelperToken != "",
		policyVersion:  policy,
		updatedAt:      now,
		dispatches:     make(map[string]DispatchOutcome),
	}
}

func (s *Session) Status(now time.Time) Status {
	if s == nil {
		return Status{}
	}
	_ = now
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := 0
	for _, outcome := range s.dispatches {
		if outcome == DispatchIndeterminate {
			pending++
		}
	}
	status := Status{
		State:                s.state,
		TransportID:          s.transportID,
		Protocol:             s.protocol,
		HelperAttached:       s.helperAttached,
		DisconnectedReason:   s.disconnectReason,
		PolicyVersion:        s.policyVersion,
		PendingIndeterminate: pending,
		HelperTokenIsLease:   false,
		TransportIDIsLease:   false,
		UpdatedAt:            s.updatedAt,
	}
	if s.helperRotatedAt != nil {
		copyTime := *s.helperRotatedAt
		status.HelperTokenRotatedAt = &copyTime
	}
	return status
}

func (s *Session) AuthorizeAction(risk action.RiskClass, now time.Time) error {
	if s == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "connection session is required")
	}
	s.mu.Lock()
	policy := s.policyVersion
	s.mu.Unlock()
	return s.AuthorizeActionWithPolicy(risk, policy, now)
}

func (s *Session) AuthorizeActionWithPolicy(risk action.RiskClass, policyVersion uint64, now time.Time) error {
	if s == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "connection session is required")
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updatedAt = now
	if policyVersion != s.policyVersion {
		return platformerrors.New(platformerrors.CodePolicyDenied, "stale policy version is refused")
	}
	if s.state != RuntimeConnected && (risk == action.RiskHigh || risk == action.RiskIrreversible || risk == action.RiskMedium) {
		return platformerrors.New(platformerrors.CodePolicyDenied, "high-risk or mutating actions are refused while disconnected or reconnecting")
	}
	if s.state == RuntimeDisconnected && risk != action.RiskLow {
		return platformerrors.New(platformerrors.CodePolicyDenied, "only low-risk observation is allowed while disconnected")
	}
	return nil
}

func (s *Session) MarkDisconnected(now time.Time, reason string) {
	if s == nil {
		return
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = RuntimeDisconnected
	s.disconnectReason = reason
	s.updatedAt = now
	for key, outcome := range s.dispatches {
		if outcome == DispatchAccepted {
			s.dispatches[key] = DispatchIndeterminate
		}
	}
}

func (s *Session) BeginReconnect(now time.Time) {
	if s == nil {
		return
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = RuntimeReconnecting
	s.updatedAt = now
}

func (s *Session) CompleteReconnect(evidence ReconnectEvidence, now time.Time) error {
	if s == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "connection session is required")
	}
	if evidence.TransportID == "" || evidence.Protocol == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "reconnect transport identity and protocol are required")
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = RuntimeConnected
	s.transportID = evidence.TransportID
	s.protocol = evidence.Protocol
	s.disconnectReason = ""
	s.updatedAt = now
	return nil
}

func (s *Session) RecordDispatch(actionID string, risk action.RiskClass, now time.Time) (DispatchOutcome, error) {
	if s == nil {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "connection session is required")
	}
	if actionID == "" {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "action id is required")
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.policyVersion == 0 {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "policy version is required")
	}
	if s.state != RuntimeConnected && (risk == action.RiskHigh || risk == action.RiskIrreversible || risk == action.RiskMedium) {
		return "", platformerrors.New(platformerrors.CodePolicyDenied, "high-risk or mutating actions are refused while disconnected or reconnecting")
	}
	if s.state == RuntimeDisconnected && risk != action.RiskLow {
		return "", platformerrors.New(platformerrors.CodePolicyDenied, "only low-risk observation is allowed while disconnected")
	}
	if s.state != RuntimeConnected {
		return "", platformerrors.New(platformerrors.CodePolicyDenied, "dispatches are refused while disconnected or reconnecting")
	}
	s.dispatches[actionID] = DispatchAccepted
	s.updatedAt = now
	return DispatchAccepted, nil
}

func (s *Session) ClassifyAfterDisconnect(actionID string, now time.Time) (DispatchOutcome, error) {
	if s == nil {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "connection session is required")
	}
	_ = now
	s.mu.Lock()
	defer s.mu.Unlock()
	outcome, ok := s.dispatches[actionID]
	if !ok {
		return "", platformerrors.New(platformerrors.CodeNotFound, "dispatch not found")
	}
	if outcome == DispatchAccepted {
		outcome = DispatchIndeterminate
		s.dispatches[actionID] = outcome
	}
	return outcome, nil
}

func (s *Session) RetryBlind(actionID string, now time.Time) error {
	if s == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "connection session is required")
	}
	_ = now
	s.mu.Lock()
	defer s.mu.Unlock()
	outcome, ok := s.dispatches[actionID]
	if !ok {
		return platformerrors.New(platformerrors.CodeNotFound, "dispatch not found")
	}
	if outcome == DispatchIndeterminate || outcome == DispatchAccepted {
		return platformerrors.New(platformerrors.CodeIndeterminateCompletion, "blind retry of indeterminate actions is refused; require fresh observation or operator confirmation")
	}
	return platformerrors.New(platformerrors.CodeConflict, "action is not eligible for retry")
}

func (s *Session) ResolveIndeterminate(actionID string, kind ResolutionKind, now time.Time) error {
	if s == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "connection session is required")
	}
	if kind != ResolutionFreshObservation && kind != ResolutionOperatorConfirmed {
		return platformerrors.New(platformerrors.CodeInvalidInput, "resolution kind is invalid")
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	outcome, ok := s.dispatches[actionID]
	if !ok {
		return platformerrors.New(platformerrors.CodeNotFound, "dispatch not found")
	}
	if outcome != DispatchIndeterminate {
		return platformerrors.New(platformerrors.CodeConflict, "dispatch is not indeterminate")
	}
	s.dispatches[actionID] = DispatchResolved
	s.updatedAt = now
	return nil
}

func (s *Session) IndeterminateActionIDs() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0)
	for actionID, outcome := range s.dispatches {
		if outcome == DispatchIndeterminate {
			ids = append(ids, actionID)
		}
	}
	return ids
}

func (s *Session) PendingIndeterminate() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := 0
	for _, outcome := range s.dispatches {
		if outcome == DispatchIndeterminate {
			pending++
		}
	}
	return pending
}

func (s *Session) ObserveHelperRotation(now time.Time) {
	if s == nil {
		return
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.helperRotatedAt = &now
	s.helperAttached = true
	s.updatedAt = now
}
