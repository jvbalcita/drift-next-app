package discovery

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"drift.local/drift-next/internal/networkprofiles"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// RuntimeDevice is one transport observation from an authorized local runtime.
// It is never a canonical device and never auto-registered.
type RuntimeDevice struct {
	Serial      string
	Host        string
	Port        uint16
	TransportID string
	Fingerprint string
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
// authorized local runtime in lab mode. It never installs tools, opens
// firewall exposure, or registers devices.
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

func (s *AuthorizedLabScanner) Scan(ctx context.Context, profile networkprofiles.NetworkProfile) ([]ObservedCandidate, error) {
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

	devices, err := s.enumerator.Enumerate(ctx)
	if err != nil {
		return nil, err
	}

	allowed := make(map[uint16]struct{}, len(profile.Ports))
	for _, port := range profile.Ports {
		allowed[port] = struct{}{}
	}

	out := make([]ObservedCandidate, 0, len(devices))
	for _, device := range devices {
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
		key := device.Serial
		if key == "" {
			key = fmt.Sprintf("%s:%d", device.Host, device.Port)
		}
		port := device.Port
		if usb {
			// USB candidates carry no TCP port; record a sentinel allowed by
			// ObservedCandidate.Valid via the serial identity path.
			port = 1
		}
		evidence := map[string]string{
			"source":       "authorized_lab_runtime",
			"transport_id": device.TransportID,
			"port":         strconv.FormatUint(uint64(device.Port), 10),
			"connection":   map[bool]string{true: "usb", false: "tcp"}[usb],
		}
		out = append(out, ObservedCandidate{
			CandidateKey: key,
			Host:         device.Host,
			Port:         port,
			Serial:       device.Serial,
			Fingerprint:  device.Fingerprint,
			Evidence:     evidence,
		})
	}
	return out, nil
}
