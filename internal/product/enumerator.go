package product

import (
	"context"
	"strings"

	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/endpoints"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// LabRuntimeEnumerator maps authorized lab discovery onto the Network Profile
// scanner seam. It never registers devices and never uses FakeScanner.
type LabRuntimeEnumerator struct {
	lab        *lab.Service
	operatorID string
}

func NewLabRuntimeEnumerator(service *lab.Service) *LabRuntimeEnumerator {
	return &LabRuntimeEnumerator{lab: service, operatorID: "control-plane"}
}

func (e *LabRuntimeEnumerator) Enumerate(ctx context.Context) ([]discovery.RuntimeDevice, error) {
	if e == nil || e.lab == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "lab runtime enumerator is required")
	}
	status, err := e.lab.Discover(ctx, e.operatorID)
	if err != nil {
		return nil, err
	}
	out := make([]discovery.RuntimeDevice, 0, len(status.Discovered))
	for _, device := range status.Discovered {
		out = append(out, runtimeDevice(device))
	}
	return out, nil
}

// runtimeDevice preserves the transport facts the adapter already observed.
// A TCP adb serial is the address the device answers on; dropping it here turns
// that transport into the USB sentinel (serial with no host and port), which in
// turn bypasses Network Profile host bounds and makes the console's USB filter
// report network devices as USB. The mapper is also where adapter auth state is
// translated to the discovery model, so scans can persist the same observation
// the runtime reported instead of silently losing it at the seam.
func runtimeDevice(device adb.DiscoveredDevice) discovery.RuntimeDevice {
	host, port := "", uint16(0)
	if device.ConnectionType == adb.ConnectionTCP {
		if parsedHost, parsedPort, ok := endpoints.SplitAddress(device.Serial); ok {
			host, port = strings.TrimSpace(parsedHost), parsedPort
		}
	}
	return discovery.RuntimeDevice{
		Serial:      device.Serial,
		Host:        host,
		Port:        port,
		TransportID: device.TransportID,
		Model:       device.Model,
		DeviceName:  device.DeviceName,
		State:       runtimeLinkState(device.State),
		// The device's own serial is identity evidence the adapter read, not a
		// transport fact this mapper derives: it is copied through so the
		// registry can match a device across a transport change.
		HardwareSerial: device.HardwareSerial,
	}
}

func runtimeLinkState(state adb.DeviceAuthState) discovery.DeviceLinkState {
	switch state {
	case adb.StateDevice:
		return discovery.LinkOnline
	case adb.StateOffline:
		return discovery.LinkOffline
	case adb.StateNoPermissions:
		// The adapter listed this transport and the HOST may not open it, which
		// is a different instruction to an operator from the device refusing
		// this host: a udev/ACL rule on the host is theirs to fix, and no
		// amount of prompting on the device would change it.
		return discovery.LinkNoPermissions
	default:
		// Unknown, authorizing, and every other non-usable adb state remain
		// visible as a refused transport rather than disappearing.
		return discovery.LinkUnauthorized
	}
}
