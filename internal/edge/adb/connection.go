package adb

import (
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
	// maxEndpointPort is the largest port a connect or disconnect may name.
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

// DisconnectArgv builds `adb disconnect IPv4:port`. The targeted form is the
// only one admitted: a bare `disconnect`, which drops every transport the server
// holds, is a fleet-wide action and has no place here.
func DisconnectArgv(endpoint string) ([]string, error) {
	if err := ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	return []string{"disconnect", endpoint}, nil
}

// KillServerArgv builds `adb kill-server`. It has no variable position at all:
// the array is three fixed literals or it is not this command.
func KillServerArgv() []string { return []string{"kill-server"} }

// StartServerArgv builds `adb start-server`. It has no variable position at all.
func StartServerArgv() []string { return []string{"start-server"} }

// matchesHostAllowlist recognises exactly the four connection-management arrays
// this adapter issues. It is a recogniser of its own, like the device-input and
// read-only ones, because a safety property can only be asserted if both sides of
// it can be asked independently in a test.
//
// The operation name it returns is deliberately not an action-catalog kind: these
// are reads-or-recovery of the transport layer, and none of them may be selected
// as an operator action through the catalog.
func matchesHostAllowlist(args []string) (string, bool) {
	switch {
	case len(args) == 2 && args[0] == "connect" && ValidateEndpoint(args[1]) == nil:
		return "connect", true
	case len(args) == 2 && args[0] == "disconnect" && ValidateEndpoint(args[1]) == nil:
		return "disconnect", true
	case len(args) == 1 && args[0] == "kill-server":
		return "kill-server", true
	case len(args) == 1 && args[0] == "start-server":
		return "start-server", true
	default:
		return "", false
	}
}
