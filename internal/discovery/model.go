// Package discovery owns non-authoritative scan history and the devices a scan
// observed. A scan is an observation that upserts canonical devices; it is not a
// candidate lifecycle and has no approval step.
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

type ScanRunState string

const (
	ScanRequested ScanRunState = "requested"
	ScanRunning   ScanRunState = "running"
	ScanCompleted ScanRunState = "completed"
	ScanFailed    ScanRunState = "failed"
	ScanCancelled ScanRunState = "cancelled"
)

// DeviceLinkState is the transport state a scan observed for a device. It is
// transport fact, never stable identity, and it is the vocabulary the endpoint
// record stores about the observation that produced it.
type DeviceLinkState string

const (
	LinkOnline       DeviceLinkState = "online"
	LinkOffline      DeviceLinkState = "offline"
	LinkUnauthorized DeviceLinkState = "unauthorized"
	// LinkNoPermissions is a transport the adapter listed but this host may not
	// open at all. It is kept apart from LinkUnauthorized because the two need
	// different things done: `no permissions` is a host-side condition an
	// operator fixes on the HOST, while an unauthorized device can only be
	// authorized on the DEVICE's own display.
	LinkNoPermissions DeviceLinkState = "no_permissions"
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

// ObservedDevice is one device a scan saw. Stable device identity (DeviceID)
// stays distinct from mutable endpoint identity (EndpointID): a re-scan of a
// known serial reuses the existing device_id and only its endpoint changes.
type ObservedDevice struct {
	Host        string
	Port        uint16
	Serial      string
	Model       string
	DeviceName  string
	Fingerprint string
	State       DeviceLinkState
	Evidence    map[string]string

	// HardwareSerial is the serial the DEVICE reported about itself (Android
	// ro.serialno) when it reported a usable one, and it is identity evidence
	// rather than a transport fact. A TCP device's Serial is the address it
	// answers on, which changes with the transport; this stays with the device,
	// so the registry matches on it first and keeps one device on one identity
	// across a new transport, a new address, or a reconnect (AGENTS.md section
	// 2: stable device identity is separate from mutable transport identity).
	// It is empty for a device that reported none - a fake device, a transport
	// adb lists as offline - which keeps that observation matching by its
	// transport exactly as it did before this field existed.
	HardwareSerial string

	// Populated once the observation has been persisted.
	DeviceID   devices.DeviceID
	EndpointID string
	Known      bool
	LastSeenAt time.Time
}

// Valid reports whether a scan produced enough to identify a transport. A USB
// transport carries a serial and no TCP port.
func (d ObservedDevice) Valid() bool {
	return d.Serial != "" || d.Host != ""
}

// Actionable reports whether the observed link permits device-scoped work. An
// offline or unauthorized device is reported, never silently actionable.
//
// It is NOT the test for whether an observation belongs in the registry, and it
// is not the test for whether an observation's transport is the device's
// current one: an attached-but-unauthorized unit is a device this plane has
// observed at a transport it can see, and reporting it is exactly what an
// operator needs (ARC-196).
func (d ObservedDevice) Actionable() bool {
	return d.State == LinkOnline
}

func (d ObservedDevice) EvidenceJSON() string {
	if len(d.Evidence) == 0 {
		return "{}"
	}
	data, err := json.Marshal(d.Evidence)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func (s DeviceLinkState) Valid() bool {
	switch s {
	case LinkOnline, LinkOffline, LinkUnauthorized, LinkNoPermissions:
		return true
	default:
		return false
	}
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
