package registration

import (
	"context"
	"strings"

	"drift.local/drift-next/internal/edge/lab"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// LabStatusSource is the narrow observation seam used by the server-side probe.
// It never accepts client-attested prerequisite booleans.
type LabStatusSource interface {
	Status(ctx context.Context) lab.Status
}

// LabStatusProbe derives provisioning prerequisites from the confirmed lab
// adapter status. Allowed ports come from server configuration, not the client.
type LabStatusProbe struct {
	Source       LabStatusSource
	AllowedPorts []uint16
}

func (p LabStatusProbe) Probe(ctx context.Context, target TargetIdentity) (ProbeResult, error) {
	if p.Source == nil {
		return ProbeResult{}, platformerrors.New(platformerrors.CodeInvalidInput, "lab status source is required")
	}
	status := p.Source.Status(ctx)
	notes := make([]string, 0, 8)

	if status.ConfirmedSerial == "" {
		return ProbeResult{}, platformerrors.New(platformerrors.CodePreconditionFailed, "confirm the lab target before provisioning verification")
	}
	if status.ConfirmedSerial != strings.TrimSpace(target.Serial) {
		return ProbeResult{}, platformerrors.New(platformerrors.CodePreconditionFailed, "provisioning serial must match the confirmed lab target")
	}
	notes = append(notes, "confirmed lab target matches serial")

	if status.TransportID == "" || status.TransportID != strings.TrimSpace(target.TransportID) {
		return ProbeResult{}, platformerrors.New(platformerrors.CodePreconditionFailed, "transport identity is missing or does not match the confirmed target")
	}
	notes = append(notes, "transport identity matches confirmed target")

	pairingOK := status.Readiness == lab.ReadinessReady && status.ConnectionState != "" && status.ConnectionState != "detached"
	adbOwned := status.Mode == lab.ModeLab || status.Mode == lab.ModeMock
	toolsOK := strings.TrimSpace(status.PlatformToolsVersion) != "" || status.Mode == lab.ModeMock
	rollbackOK := !status.Indeterminate && status.FailureClass == ""

	portOK := true
	if target.ConnectionType == "wireless" || target.EndpointPort > 0 {
		portOK = portAllowed(target.EndpointPort, p.AllowedPorts)
	}

	result := ProbeResult{
		PairingAuthorized:       pairingOK,
		ADBServerOwned:          adbOwned,
		PlatformToolsCompatible: toolsOK,
		PortPolicyAllowed:       portOK,
		RollbackReady:           rollbackOK,
		Notes:                   notes,
	}
	if status.Mode == lab.ModeMock {
		result.Notes = append(result.Notes, "mock lab mode probe; not a real-device measurement")
	}
	return result, nil
}
