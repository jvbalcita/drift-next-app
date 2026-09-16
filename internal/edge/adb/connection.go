package adb

import (
	"strconv"
	"strings"

	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// The host-level admissions of this adapter. These are the connection-management
// commands: they act on the adb server and its transports rather than on a device,
// so they carry no serial and their arrays never begin with `shell`. That is also
// what keeps them disjoint from the device-scoped admissions by construction
// rather than by review.
//
// The variable position in an endpoint is a canonical `IPv4:port`. Nothing else
// can pass: every octet and the port are validated as canonical bounded decimals,
// the value is split on exactly one colon, and no other character is admitted, so
// an endpoint can never carry whitespace, a quote, a flag, a path or a shell
// metacharacter. A hostname, an mDNS name and an IPv6 literal are refused rather
// than passed through to adb, because none of them can be validated by this rule
// and an unvalidated token is exactly what an allow-list exists to refuse.
const (
	// maxEndpointOctet is the largest value an IPv4 octet may carry.
	maxEndpointOctet = 255
	// maxEndpointPort is the largest port a connect may name.
	maxEndpointPort = 65535
)

// ValidateEndpoint reports whether value is the canonical `IPv4:port` this
// adapter's connection admissions accept.
func ValidateEndpoint(value string) error {
	host, port, found := strings.Cut(value, ":")
	if !found || strings.Contains(port, ":") {
		return platformerrors.New(platformerrors.CodeInvalidInput, "an endpoint must be IPv4:port")
	}
	octets := strings.Split(host, ".")
	if len(octets) != 4 {
		return platformerrors.New(platformerrors.CodeInvalidInput, "an endpoint host must be an IPv4 address")
	}
	for _, octet := range octets {
		if !isBoundedDecimal(octet, maxEndpointOctet) {
			return platformerrors.New(platformerrors.CodeInvalidInput, "an endpoint host must be an IPv4 address")
		}
	}
	if !isBoundedDecimalBetween(port, 1, maxEndpointPort) {
		return platformerrors.New(platformerrors.CodeInvalidInput, "an endpoint port must be between 1 and 65535")
	}
	return nil
}

// ConnectArgv builds `adb connect IPv4:port`.
func ConnectArgv(endpoint string) ([]string, error) {
	if err := ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	return []string{"connect", endpoint}, nil
}

// EndpointPort reads the port out of an endpoint, so that a port decision is made
// on a value this package's own rule has already validated rather than on a number
// parsed out of arbitrary text.
func EndpointPort(endpoint string) (uint16, error) {
	if err := ValidateEndpoint(endpoint); err != nil {
		return 0, err
	}
	_, port, _ := strings.Cut(endpoint, ":")
	value, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return 0, platformerrors.New(platformerrors.CodeInvalidInput, "an endpoint port must be between 1 and 65535")
	}
	return uint16(value), nil
}

// KillServerArgv builds `adb kill-server`. It has no variable position at all:
// the array is three fixed literals or it is not this command.
func KillServerArgv() []string { return []string{"kill-server"} }

// StartServerArgv builds `adb start-server`. It has no variable position at all.
func StartServerArgv() []string { return []string{"start-server"} }

// matchesHostAllowlist recognises exactly the three connection-management arrays
// this adapter issues: `connect`, `kill-server` and `start-server`. It is a
// recogniser of its own, like the device-input and read-only ones, because a safety
// property can only be asserted if both sides of it can be asked independently in a
// test.
//
// There is deliberately NO `disconnect`. It removes the host's transport record
// while leaving the device itself reachable, so a "recovered" state reached through
// it is a state the product fabricated: the fleet would read as unreachable while
// the hardware is fine, and a recovery proven against that has proven nothing about
// the failures that actually occur. The real ones are a device going away, adbd
// restarting, the network dropping, or the adb server not running — and the last is
// produced honestly by `kill-server` + `start-server`, which the recovery path uses.
// Re-adding a targeted disconnect needs its own case argued on its own card.
//
// The operation name it returns is deliberately not an action-catalog kind: these
// are transport-layer recovery, and none of them may be selected as an operator
// action through the catalog.
func matchesHostAllowlist(args []string) (string, bool) {
	switch {
	case len(args) == 2 && args[0] == "connect" && ValidateEndpoint(args[1]) == nil:
		return "connect", true
	case len(args) == 1 && args[0] == "kill-server":
		return "kill-server", true
	case len(args) == 1 && args[0] == "start-server":
		return "start-server", true
	default:
		return "", false
	}
}
