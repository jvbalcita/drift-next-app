// Package networkprofiles owns bounded, operator-approved discovery policy.
package networkprofiles

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type NetworkProfileID string

// NetworkProfile is deliberately bounded saved discovery policy.
type NetworkProfile struct {
	ID            NetworkProfileID
	Workspace     organizations.WorkspaceID
	Name          string
	AddressPolicy string
	Ports         []uint16
	IsDefault     bool
}

const (
	maxAddressPolicyBytes = 256
	maxProfilePorts       = 64
)

// Validate checks the bounded, non-authoritative discovery policy. It never
// opens a socket or otherwise performs discovery.
func (p NetworkProfile) Validate() error {
	if strings.TrimSpace(string(p.ID)) == "" || strings.TrimSpace(string(p.Workspace)) == "" {
		return fmt.Errorf("network profile identity is required")
	}
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 128 {
		return fmt.Errorf("network profile name is required and bounded")
	}
	policy := strings.TrimSpace(p.AddressPolicy)
	if policy == "" || len(policy) > maxAddressPolicyBytes {
		return fmt.Errorf("address policy is required and bounded")
	}
	if !validAddressPolicy(policy) {
		return fmt.Errorf("address policy must be a CIDR or an inclusive IP range")
	}
	if len(p.Ports) == 0 || len(p.Ports) > maxProfilePorts {
		return fmt.Errorf("at least one and at most %d ports are required", maxProfilePorts)
	}
	seen := make(map[uint16]struct{}, len(p.Ports))
	for _, port := range p.Ports {
		if port == 0 {
			return fmt.Errorf("ports must be between 1 and 65535")
		}
		if _, ok := seen[port]; ok {
			return fmt.Errorf("ports must not contain duplicates")
		}
		seen[port] = struct{}{}
	}
	return nil
}

// EnteredRange builds the scan target for an inclusive IPv4 range the operator
// ENTERED in the console. It is a scan target, not saved policy: nothing
// persists it, and it carries no Network Profile row. It is spelled as the value
// the scan adapter already reads, so an entered range and a saved range profile
// are bounded by the same check and scanned by the same code rather than by a
// second implementation of the range rule.
//
// Its identity and name exist only so the value satisfies Validate, which is the
// check the adapter applies. A malformed entry is refused as invalid input with a
// named reason rather than reaching a scan.
func EnteredRange(workspace organizations.WorkspaceID, addressPolicy string, port uint16) (NetworkProfile, error) {
	target := NetworkProfile{
		ID:            enteredRangeProfileID,
		Workspace:     workspace,
		Name:          "Entered range",
		AddressPolicy: strings.TrimSpace(addressPolicy),
		Ports:         []uint16{port},
	}
	if err := target.Validate(); err != nil {
		return NetworkProfile{}, platformerrors.New(platformerrors.CodeInvalidInput, "the entered range must be an IPv4 range of four octets each 0 through 255 whose start is at or before its end, scanned on a port between 1 and 65535")
	}
	return target, nil
}

// enteredRangeProfileID is the identity an entered range carries in memory. It is
// deliberately not a UUID: a value that could be mistaken for a stored profile id
// is the wrong thing to hand a scan run that records no profile reference.
const enteredRangeProfileID NetworkProfileID = "entered-range"

func validAddressPolicy(policy string) bool {
	if ip, network, err := net.ParseCIDR(policy); err == nil {
		prefix, bits := network.Mask.Size()
		if ip.To4() != nil {
			return bits == 32 && prefix >= 16
		}
		return bits == 128 && prefix >= 64
	}
	parts := strings.Split(policy, "-")
	if len(parts) != 2 {
		return false
	}
	start, end := net.ParseIP(strings.TrimSpace(parts[0])), net.ParseIP(strings.TrimSpace(parts[1]))
	if start == nil || end == nil || start.To4() == nil || end.To4() == nil {
		return false
	}
	return bytesCompare(start.To4(), end.To4()) <= 0
}

func bytesCompare(left, right []byte) int {
	for i := range left {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	return 0
}

// ContainsHost reports whether host falls inside the bounded AddressPolicy.
// USB / empty hosts are not evaluated against CIDR policy.
func (p NetworkProfile) ContainsHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	policy := strings.TrimSpace(p.AddressPolicy)
	if _, network, err := net.ParseCIDR(policy); err == nil {
		return network.Contains(ip)
	}
	parts := strings.Split(policy, "-")
	if len(parts) != 2 {
		return false
	}
	start, end := net.ParseIP(strings.TrimSpace(parts[0])), net.ParseIP(strings.TrimSpace(parts[1]))
	if start == nil || end == nil || ip.To4() == nil || start.To4() == nil || end.To4() == nil {
		return false
	}
	v4 := ip.To4()
	return bytesCompare(start.To4(), v4) <= 0 && bytesCompare(v4, end.To4()) <= 0
}

// SortedPorts returns a copy suitable for deterministic persistence and
// request hashing.
func (p NetworkProfile) SortedPorts() []uint16 {
	ports := append([]uint16(nil), p.Ports...)
	sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })
	return ports
}
