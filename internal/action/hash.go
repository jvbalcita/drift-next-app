package action

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// RequestHash returns a stable digest of the typed action intent. It includes
// semantic target hints but has no field for raw coordinates, credentials, or
// arbitrary protocol payloads.
func RequestHash(intent Intent) (string, error) {
	type hashInput struct {
		Workspace         string
		DeviceID          string
		Kind              Kind
		Target            SemanticTarget
		ObservationToken  string
		InvocationSurface InvocationSurface
		Capabilities      []Capability
		ApprovalGranted   bool
		TimeoutNanos      int64
	}
	capabilities := append([]Capability(nil), intent.Capabilities...)
	sort.Slice(capabilities, func(left, right int) bool { return capabilities[left] < capabilities[right] })
	payload, err := json.Marshal(hashInput{
		Workspace:         intent.Workspace,
		DeviceID:          intent.DeviceID,
		Kind:              intent.Kind,
		Target:            intent.Target,
		ObservationToken:  intent.ObservationToken,
		InvocationSurface: intent.InvocationSurface,
		Capabilities:      capabilities,
		ApprovalGranted:   intent.ApprovalGranted,
		TimeoutNanos:      intent.Timeout.Nanoseconds(),
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
