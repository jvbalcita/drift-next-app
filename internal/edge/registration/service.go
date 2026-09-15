// Package registration owns controlled one-device lab provisioning verification
// and explicit operator-approved registration transitions. It never silently
// registers a discovered endpoint and never opens a second database.
package registration

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type State string

const (
	StateDiscovered        State = "discovered"
	StateProvisionVerified State = "provision_verified"
	StateApproved          State = "approved"
	StateRegistered        State = "registered"
)

// TargetIdentity is the operator-supplied identity under verification. Boolean
// prerequisite outcomes are never taken from this struct; they come only from
// PrerequisiteProbe.
type TargetIdentity struct {
	Serial         string
	TransportID    string
	EndpointHost   string
	EndpointPort   uint16
	ConnectionType string
	AllowedPorts   []uint16
	ActorID        string
}

// ProbeResult is the sanitized outcome of a server-side prerequisite probe.
type ProbeResult struct {
	PairingAuthorized       bool
	ADBServerOwned          bool
	PlatformToolsCompatible bool
	PortPolicyAllowed       bool
	RollbackReady           bool
	Notes                   []string
}

// PrerequisiteProbe performs runtime checks. Implementations must not trust
// client-attested booleans; fakes return deterministic probe results for tests.
type PrerequisiteProbe interface {
	Probe(ctx context.Context, target TargetIdentity) (ProbeResult, error)
}

type ProvisionReady struct {
	Serial      string
	TransportID string
	State       State
	Ready       bool
	CheckedAt   time.Time
	Notes       []string
}

type Approval struct {
	Serial    string
	ActorID   string
	Reason    string
	DecidedAt time.Time
}

type RegisterRequest struct {
	Serial      string
	DisplayName string
	ActorID     string
}

type RegisterResult struct {
	DeviceID    string
	EndpointID  string
	Serial      string
	DisplayName string
	State       State
	OccurredAt  time.Time
}

type Config struct {
	MaxRegisteredDevices int
	Probe                PrerequisiteProbe
}

type Service struct {
	mu           sync.Mutex
	maxDevices   int
	probe        PrerequisiteProbe
	verified     map[string]ProvisionReady
	approvals    map[string]Approval
	registered   map[string]RegisterResult
	nextDevice   int
	nextEndpoint int
}

// AttestedProbe is a test double that returns fixed probe results. Production
// composition must use a runtime-backed probe, never client form fields.
type AttestedProbe struct {
	Result ProbeResult
	Err    error
}

func (p AttestedProbe) Probe(_ context.Context, _ TargetIdentity) (ProbeResult, error) {
	if p.Err != nil {
		return ProbeResult{}, p.Err
	}
	return p.Result, nil
}

func NewService(cfg Config) *Service {
	max := cfg.MaxRegisteredDevices
	if max <= 0 {
		max = 1
	}
	return &Service{
		maxDevices:   max,
		probe:        cfg.Probe,
		verified:     make(map[string]ProvisionReady),
		approvals:    make(map[string]Approval),
		registered:   make(map[string]RegisterResult),
		nextDevice:   1,
		nextEndpoint: 1,
	}
}

func (s *Service) VerifyProvisioning(ctx context.Context, target TargetIdentity, now time.Time) (ProvisionReady, error) {
	if s == nil || s.probe == nil {
		return ProvisionReady{}, platformerrors.New(platformerrors.CodeInvalidInput, "registration service and prerequisite probe are required")
	}
	now = now.UTC()
	serial := strings.TrimSpace(target.Serial)
	transport := strings.TrimSpace(target.TransportID)
	actor := strings.TrimSpace(target.ActorID)
	if serial == "" {
		return ProvisionReady{}, platformerrors.New(platformerrors.CodeInvalidInput, "endpoint serial identity is required")
	}
	if transport == "" {
		return ProvisionReady{}, platformerrors.New(platformerrors.CodeInvalidInput, "transport identity is required")
	}
	if actor == "" {
		return ProvisionReady{}, platformerrors.New(platformerrors.CodePolicyDenied, "operator authorization is required for provisioning")
	}

	probe, err := s.probe.Probe(ctx, target)
	if err != nil {
		return ProvisionReady{}, err
	}

	notes := append([]string(nil), probe.Notes...)
	fail := func(msg string) (ProvisionReady, error) {
		return ProvisionReady{}, platformerrors.New(platformerrors.CodePreconditionFailed, msg)
	}
	if !probe.PairingAuthorized {
		return fail("device pairing and authorization are not verified")
	}
	notes = append(notes, "pairing authorized")
	if !probe.ADBServerOwned {
		return fail("ADB server ownership is not verified")
	}
	notes = append(notes, "adb server ownership verified")
	if !probe.PlatformToolsCompatible {
		return fail("platform-tools are missing or incompatible; install or repair is not performed automatically")
	}
	notes = append(notes, "platform-tools compatible")
	if !probe.PortPolicyAllowed {
		return fail("port policy validation failed")
	}
	if target.ConnectionType == "wireless" || target.EndpointPort > 0 {
		if !portAllowed(target.EndpointPort, target.AllowedPorts) {
			return fail("endpoint port is outside the allowed port policy")
		}
		notes = append(notes, fmt.Sprintf("port %d allowed", target.EndpointPort))
	} else {
		notes = append(notes, "usb transport; no tcp port required")
	}
	if !probe.RollbackReady {
		return fail("rollback readiness is not recorded")
	}
	notes = append(notes, "rollback ready")

	ready := ProvisionReady{
		Serial:      serial,
		TransportID: transport,
		State:       StateProvisionVerified,
		Ready:       true,
		CheckedAt:   now,
		Notes:       notes,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.registered[serial]; ok {
		return ProvisionReady{
			Serial:      existing.Serial,
			TransportID: transport,
			State:       StateRegistered,
			Ready:       true,
			CheckedAt:   now,
			Notes:       append(notes, "already registered; verification is idempotent"),
		}, nil
	}
	s.verified[serial] = ready
	delete(s.approvals, serial) // re-verify clears prior approval
	return ready, nil
}

// Approve records a durable operator approval distinct from provisioning and
// registration. Registration cannot proceed without this transition.
func (s *Service) Approve(serial, actorID, reason string, now time.Time) (Approval, error) {
	if s == nil {
		return Approval{}, platformerrors.New(platformerrors.CodeInvalidInput, "registration service is required")
	}
	now = now.UTC()
	serial = strings.TrimSpace(serial)
	actorID = strings.TrimSpace(actorID)
	reason = strings.TrimSpace(reason)
	if serial == "" || actorID == "" || reason == "" {
		return Approval{}, platformerrors.New(platformerrors.CodeInvalidInput, "serial, actor, and approval reason are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.registered[serial]; ok {
		return Approval{}, platformerrors.New(platformerrors.CodeConflict, "serial is already registered")
	}
	if ready, ok := s.verified[serial]; !ok || !ready.Ready {
		return Approval{}, platformerrors.New(platformerrors.CodePreconditionFailed, "provisioning verification is required before approval")
	}
	approval := Approval{Serial: serial, ActorID: actorID, Reason: reason, DecidedAt: now}
	s.approvals[serial] = approval
	return approval, nil
}

func (s *Service) Register(req RegisterRequest, now time.Time) (RegisterResult, error) {
	if s == nil {
		return RegisterResult{}, platformerrors.New(platformerrors.CodeInvalidInput, "registration service is required")
	}
	now = now.UTC()
	serial := strings.TrimSpace(req.Serial)
	if serial == "" || strings.TrimSpace(req.DisplayName) == "" || strings.TrimSpace(req.ActorID) == "" {
		return RegisterResult{}, platformerrors.New(platformerrors.CodeInvalidInput, "serial, display name, and actor are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.registered[serial]; ok {
		return existing, nil
	}
	if _, ok := s.verified[serial]; !ok {
		return RegisterResult{}, platformerrors.New(platformerrors.CodePreconditionFailed, "provisioning verification is required before registration")
	}
	if _, ok := s.approvals[serial]; !ok {
		return RegisterResult{}, platformerrors.New(platformerrors.CodePolicyDenied, "operator approval is required before registration")
	}
	if len(s.registered) >= s.maxDevices {
		return RegisterResult{}, platformerrors.New(platformerrors.CodePolicyDenied, "one-device lab registration scope is exhausted")
	}

	result := RegisterResult{
		DeviceID:    fmt.Sprintf("device-lab-%d", s.nextDevice),
		EndpointID:  fmt.Sprintf("endpoint-lab-%d", s.nextEndpoint),
		Serial:      serial,
		DisplayName: strings.TrimSpace(req.DisplayName),
		State:       StateRegistered,
		OccurredAt:  now,
	}
	s.nextDevice++
	s.nextEndpoint++
	s.registered[serial] = result
	return result, nil
}

// Lookup returns the durable provisioning and registration projections for a
// serial. An empty serial returns the sole registered lab device when present.
func (s *Service) Lookup(serial string) (ProvisionReady, RegisterResult, bool, bool) {
	if s == nil {
		return ProvisionReady{}, RegisterResult{}, false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	serial = strings.TrimSpace(serial)
	if serial == "" {
		for _, registered := range s.registered {
			return ProvisionReady{
				Serial: registered.Serial,
				State:  StateRegistered,
				Ready:  true,
			}, registered, true, true
		}
		return ProvisionReady{}, RegisterResult{}, false, false
	}
	if registered, ok := s.registered[serial]; ok {
		return ProvisionReady{
			Serial: registered.Serial,
			State:  StateRegistered,
			Ready:  true,
		}, registered, true, true
	}
	ready, hasReady := s.verified[serial]
	if !hasReady {
		return ProvisionReady{}, RegisterResult{}, false, false
	}
	if _, approved := s.approvals[serial]; approved {
		ready.State = StateApproved
	}
	return ready, RegisterResult{}, true, false
}

// RevertVerify undoes an in-memory verify when durable persistence fails.
func (s *Service) RevertVerify(serial string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.verified, strings.TrimSpace(serial))
}

// RevertApprove undoes an in-memory approval when durable persistence fails.
func (s *Service) RevertApprove(serial string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.approvals, strings.TrimSpace(serial))
}

// RevertRegister undoes an in-memory registration when durable persistence fails.
func (s *Service) RevertRegister(serial string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.registered, strings.TrimSpace(serial))
}

// ReplaceRegistered overwrites the in-memory registration with durable IDs.
func (s *Service) ReplaceRegistered(result RegisterResult) {
	if s == nil || strings.TrimSpace(result.Serial) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registered[strings.TrimSpace(result.Serial)] = result
}

// RestoreApproval puts an approval back after a failed durable verify that
// cleared it, so in-memory state matches the still-present SQLite approval.
func (s *Service) RestoreApproval(approval Approval) {
	if s == nil || strings.TrimSpace(approval.Serial) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvals[strings.TrimSpace(approval.Serial)] = approval
}

// Hydrate loads durable projections into memory so Approve→Register survives
// process restart when SQLite is the system of record.
func (s *Service) Hydrate(ready ProvisionReady, hasReady bool, approval Approval, hasApproval bool, registered RegisterResult, hasRegistered bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if hasRegistered && strings.TrimSpace(registered.Serial) != "" {
		serial := strings.TrimSpace(registered.Serial)
		s.registered[serial] = registered
		s.verified[serial] = ProvisionReady{
			Serial:      serial,
			TransportID: ready.TransportID,
			State:       StateRegistered,
			Ready:       true,
			CheckedAt:   ready.CheckedAt,
			Notes:       append([]string(nil), ready.Notes...),
		}
		return
	}
	if hasReady && strings.TrimSpace(ready.Serial) != "" {
		serial := strings.TrimSpace(ready.Serial)
		projected := ready
		projected.State = StateProvisionVerified
		s.verified[serial] = projected
	}
	if hasApproval && strings.TrimSpace(approval.Serial) != "" {
		s.approvals[strings.TrimSpace(approval.Serial)] = approval
	}
}

func portAllowed(port uint16, allowed []uint16) bool {
	if port == 0 {
		return false
	}
	for _, candidate := range allowed {
		if candidate == port {
			return true
		}
	}
	return false
}
