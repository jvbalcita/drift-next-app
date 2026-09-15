// Package registration owns controlled one-device lab provisioning verification
// and explicit operator-approved registration transitions. It never silently
// registers a discovered endpoint and never opens a second database.
package registration

import (
	"fmt"
	"strings"
	"sync"
	"time"

	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type State string

const (
	StateDiscovered         State = "discovered"
	StateProvisionVerified  State = "provision_verified"
	StateRegistered         State = "registered"
)

type ProvisionEvidence struct {
	Serial                  string
	TransportID             string
	EndpointHost            string
	EndpointPort            uint16
	ConnectionType          string
	PairingAuthorized       bool
	ADBServerOwned          bool
	PlatformToolsCompatible bool
	PortPolicyAllowed       bool
	RollbackReady           bool
	AllowedPorts            []uint16
	RequireOperatorAuth     bool
	OperatorAuthorized      bool
}

type ProvisionReady struct {
	Serial      string
	TransportID string
	State       State
	Ready       bool
	CheckedAt   time.Time
	Notes       []string
}

type RegisterRequest struct {
	Serial      string
	DisplayName string
	Approved    bool
	ActorID     string
}

type RegisterResult struct {
	DeviceID   string
	EndpointID string
	Serial     string
	State      State
	OccurredAt time.Time
}

type Config struct {
	MaxRegisteredDevices int
}

type Service struct {
	mu          sync.Mutex
	maxDevices  int
	verified    map[string]ProvisionReady
	registered  map[string]RegisterResult
	nextDevice  int
	nextEndpoint int
}

func NewService(cfg Config) *Service {
	max := cfg.MaxRegisteredDevices
	if max <= 0 {
		max = 1
	}
	return &Service{
		maxDevices:   max,
		verified:     make(map[string]ProvisionReady),
		registered:   make(map[string]RegisterResult),
		nextDevice:   1,
		nextEndpoint: 1,
	}
}

func (s *Service) VerifyProvisioning(evidence ProvisionEvidence, now time.Time) (ProvisionReady, error) {
	if s == nil {
		return ProvisionReady{}, platformerrors.New(platformerrors.CodeInvalidInput, "registration service is required")
	}
	now = now.UTC()
	serial := strings.TrimSpace(evidence.Serial)
	transport := strings.TrimSpace(evidence.TransportID)
	if serial == "" {
		return ProvisionReady{}, platformerrors.New(platformerrors.CodeInvalidInput, "endpoint serial identity is required")
	}
	if transport == "" {
		return ProvisionReady{}, platformerrors.New(platformerrors.CodeInvalidInput, "transport identity is required")
	}
	if evidence.RequireOperatorAuth && !evidence.OperatorAuthorized {
		return ProvisionReady{}, platformerrors.New(platformerrors.CodePolicyDenied, "operator authorization is required for provisioning")
	}

	notes := make([]string, 0, 6)
	fail := func(msg string) (ProvisionReady, error) {
		return ProvisionReady{}, platformerrors.New(platformerrors.CodePreconditionFailed, msg)
	}
	if !evidence.PairingAuthorized {
		return fail("device pairing and authorization are not verified")
	}
	notes = append(notes, "pairing authorized")
	if !evidence.ADBServerOwned {
		return fail("ADB server ownership is not verified")
	}
	notes = append(notes, "adb server ownership verified")
	if !evidence.PlatformToolsCompatible {
		return fail("platform-tools are missing or incompatible; install or repair is not performed automatically")
	}
	notes = append(notes, "platform-tools compatible")
	if !evidence.PortPolicyAllowed {
		return fail("port policy validation failed")
	}
	if evidence.ConnectionType == "wireless" || evidence.EndpointPort > 0 {
		if !portAllowed(evidence.EndpointPort, evidence.AllowedPorts) {
			return fail("endpoint port is outside the allowed port policy")
		}
		notes = append(notes, fmt.Sprintf("port %d allowed", evidence.EndpointPort))
	} else {
		notes = append(notes, "usb transport; no tcp port required")
	}
	if !evidence.RollbackReady {
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
	s.verified[serial] = ready
	return ready, nil
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
	if !req.Approved {
		return RegisterResult{}, platformerrors.New(platformerrors.CodePolicyDenied, "operator approval is required before registration")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.registered[serial]; ok {
		return existing, nil
	}
	if _, ok := s.verified[serial]; !ok {
		return RegisterResult{}, platformerrors.New(platformerrors.CodePreconditionFailed, "provisioning verification is required before registration")
	}
	if len(s.registered) >= s.maxDevices {
		return RegisterResult{}, platformerrors.New(platformerrors.CodePolicyDenied, "one-device lab registration scope is exhausted")
	}

	result := RegisterResult{
		DeviceID:   fmt.Sprintf("device-lab-%d", s.nextDevice),
		EndpointID: fmt.Sprintf("endpoint-lab-%d", s.nextEndpoint),
		Serial:     serial,
		State:      StateRegistered,
		OccurredAt: now,
	}
	s.nextDevice++
	s.nextEndpoint++
	s.registered[serial] = result
	return result, nil
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
