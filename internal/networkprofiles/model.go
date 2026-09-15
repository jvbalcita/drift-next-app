// Package networkprofiles owns bounded, operator-approved discovery policy.
package networkprofiles

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
)

type NetworkProfileID string

type State string

const (
	Draft    State = "draft"
	Active   State = "active"
	Disabled State = "disabled"
	Retired  State = "retired"
)

// NetworkProfile is deliberately bounded. CIDR/range and port validation is
// performed by the owning service before a profile can become active.
type NetworkProfile struct {
	ID            NetworkProfileID
	Workspace     organizations.WorkspaceID
	Name          string
	AddressPolicy string
	Ports         []uint16
	IsDefault     bool
	State         State
	RowVersion    uint64
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
	if !p.State.Valid() {
		return fmt.Errorf("network profile state is invalid")
	}
	if p.IsDefault && p.State != Active {
		return fmt.Errorf("only active profiles may be default")
	}
	return nil
}

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

func (s State) Valid() bool {
	switch s {
	case Draft, Active, Disabled, Retired:
		return true
	default:
		return false
	}
}

func CanTransition(from, to State) bool {
	switch from {
	case Draft:
		return to == Active || to == Retired
	case Active:
		return to == Disabled || to == Retired
	case Disabled:
		return to == Active || to == Retired
	case Retired:
		return false
	default:
		return false
	}
}

func Transition(from, to State) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("network_profile", string(from), string(to))
	}
	return nil
}
