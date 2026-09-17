package discovery

import (
	"context"
	"strconv"
	"strings"

	"drift.local/drift-next/internal/networkprofiles"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// RuntimeDevice is one transport observation from an authorized local runtime.
// It is never a canonical device on its own: the scan that carries it persists
// the device.
type RuntimeDevice struct {
	Serial      string
	Host        string
	Port        uint16
	TransportID string
	Model       string
	Fingerprint string
	State       DeviceLinkState
}

// RuntimeEnumerator is the narrow seam used by lab Network Profile scans.
type RuntimeEnumerator interface {
	Enumerate(ctx context.Context) ([]RuntimeDevice, error)
}

// LabScannerConfig gates Network Profile scans behind an authorized lab runtime.
type LabScannerConfig struct {
	Authorized bool
	LabMode    bool
	Enumerator RuntimeEnumerator
}

// AuthorizedLabScanner executes Network Profile scans only through an
// authorized local runtime in lab mode. It never installs tools or opens
// firewall exposure.
type AuthorizedLabScanner struct {
	authorized bool
	labMode    bool
	enumerator RuntimeEnumerator
}

func NewAuthorizedLabScanner(cfg LabScannerConfig) *AuthorizedLabScanner {
	return &AuthorizedLabScanner{
		authorized: cfg.Authorized,
		labMode:    cfg.LabMode,
		enumerator: cfg.Enumerator,
	}
}

func (s *AuthorizedLabScanner) Scan(ctx context.Context, profile networkprofiles.NetworkProfile) ([]ObservedDevice, error) {
	if s == nil || s.enumerator == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "authorized lab scanner dependencies are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.labMode || !s.authorized {
		return nil, platformerrors.New(platformerrors.CodePolicyDenied, "Network Profile scans require an authorized local runtime in lab mode")
	}
	if err := profile.Validate(); err != nil {
		return nil, err
	}

	found, err := s.enumerator.Enumerate(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]ObservedDevice, 0, len(found))
	for _, device := range found {
		if !RuntimeTransportIsUSB(device) {
			// A port of 0 with no host is not an addressable transport, so there is
			// nothing here to observe.
			if device.Port == 0 {
				continue
			}
			// The HOST RANGE is the bound. The PORT is not a filter: it is a discovered
			// fact, and the decision about it belongs at connect time, where the port
			// policy names it and refuses it. Dropping an off-port device here hides the
			// very device port activation exists for and makes Activate unreachable for
			// it — which is a correction to this function's previous behaviour, not a
			// loosening of it. The profile's Ports remain the accepted set the connect
			// policy reads; they are simply not consulted while observing.
			if !profile.ContainsHost(device.Host) {
				continue
			}
		}
		out = append(out, ObservedDeviceFromRuntime(device))
	}
	return out, nil
}

// RuntimeTransportIsUSB reports whether an enumerated transport is a USB
// attachment: the adapter named a serial and no address at all. This is decided
// in one place so the scan's filter and the observation the registry persists
// cannot disagree about the transport they are looking at.
func RuntimeTransportIsUSB(device RuntimeDevice) bool {
	return device.Port == 0 && strings.TrimSpace(device.Host) == "" && strings.TrimSpace(device.Serial) != ""
}

// ObservedDeviceFromRuntime builds the observation the registry persists from one
// enumerated runtime transport. A scan and the post-launch arrival watcher both
// build their observations here, so one enumerated transport becomes the same
// observation whichever path hands it to the registry - including the transport
// fact read from the observation's own address rather than guessed by a reader
// (AGENTS.md section 2).
func ObservedDeviceFromRuntime(device RuntimeDevice) ObservedDevice {
	return ObservedDevice{
		Host:        device.Host,
		Port:        device.Port,
		Serial:      device.Serial,
		Model:       device.Model,
		Fingerprint: device.Fingerprint,
		State:       device.State,
		Evidence: map[string]string{
			"source":       "authorized_lab_runtime",
			"transport_id": device.TransportID,
			"port":         strconv.FormatUint(uint64(device.Port), 10),
			"connection":   map[bool]string{true: "usb", false: "tcp"}[RuntimeTransportIsUSB(device)],
		},
	}
}
