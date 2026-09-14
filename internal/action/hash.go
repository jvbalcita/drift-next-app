package action

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// RequestHash returns a stable digest of the typed action intent. Coordinate
// fallbacks are included only when explicitly approved; credentials and raw
// protocol payloads have no representation in this contract.
func RequestHash(intent Intent) (string, error) {
	type hashInput struct {
		Workspace          string
		DeviceID           string
		Kind               Kind
		Target             SemanticTarget
		TextValue          string
		ValueLength        int
		Gesture            *GesturePath
		KeyCode            int
		ObservationToken   string
		InvocationSurface  InvocationSurface
		Capabilities       []Capability
		ApprovalGranted    bool
		TimeoutNanos       int64
		CoordinateFallback *CoordinateFallback
	}
	capabilities := append([]Capability(nil), intent.Capabilities...)
	sort.Slice(capabilities, func(left, right int) bool { return capabilities[left] < capabilities[right] })
	payload, err := json.Marshal(hashInput{
		Workspace:          intent.Workspace,
		DeviceID:           intent.DeviceID,
		Kind:               intent.Kind,
		Target:             intent.Target,
		TextValue:          intent.TextValue,
		ValueLength:        intent.ValueLength,
		Gesture:            intent.Gesture,
		KeyCode:            intent.KeyCode,
		ObservationToken:   intent.ObservationToken,
		InvocationSurface:  intent.InvocationSurface,
		Capabilities:       capabilities,
		ApprovalGranted:    intent.ApprovalGranted,
		TimeoutNanos:       intent.Timeout.Nanoseconds(),
		CoordinateFallback: intent.CoordinateFallback,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
