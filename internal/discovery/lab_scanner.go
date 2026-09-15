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

	allowed := make(map[uint16]struct{}, len(profile.Ports))
	for _, port := range profile.Ports {
		allowed[port] = struct{}{}
	}

	out := make([]ObservedDevice, 0, len(found))
	for _, device := range found {
		usb := device.Port == 0 && strings.TrimSpace(device.Host) == "" && strings.TrimSpace(device.Serial) != ""
		if !usb {
			if device.Port == 0 {
				continue
			}
			if _, ok := allowed[device.Port]; !ok {
				continue
			}
			if !profile.ContainsHost(device.Host) {
				continue
			}
		}
		evidence := map[string]string{
			"source":       "authorized_lab_runtime",
			"transport_id": device.TransportID,
			"port":         strconv.FormatUint(uint64(device.Port), 10),
			"connection":   map[bool]string{true: "usb", false: "tcp"}[usb],
		}
		out = append(out, ObservedDevice{
			Host:        device.Host,
			Port:        device.Port,
			Serial:      device.Serial,
			Model:       device.Model,
			Fingerprint: device.Fingerprint,
			State:       device.State,
			Evidence:    evidence,
		})
	}
	return out, nil
}
