// Package settings owns typed, explicitly scoped configuration projections.
package settings

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/redaction"
)

type SettingID string

type ValueKind string

const (
	ValueBoolean ValueKind = "boolean"
	ValueInteger ValueKind = "integer"
	ValueEnum    ValueKind = "enum"
	ValueJSON    ValueKind = "json"
)

type RiskClass string

const (
	RiskSafetyCritical RiskClass = "safety_critical"
	RiskLowPreference  RiskClass = "low_risk_preference"
)

type Definition struct {
	Key          string
	Kind         ValueKind
	Risk         RiskClass
	AllowedEnums []string
	MinInteger   int64
	MaxInteger   int64
}

var definitions = map[string]Definition{
	"require_explicit_approval": {Key: "require_explicit_approval", Kind: ValueBoolean, Risk: RiskSafetyCritical},
	"max_action_timeout_ms":     {Key: "max_action_timeout_ms", Kind: ValueInteger, Risk: RiskSafetyCritical, MinInteger: 1, MaxInteger: 300000},
	"event_retention_days":      {Key: "event_retention_days", Kind: ValueInteger, Risk: RiskSafetyCritical, MinInteger: 1, MaxInteger: 3650},
	"table_density":             {Key: "table_density", Kind: ValueEnum, Risk: RiskLowPreference, AllowedEnums: []string{"compact", "comfortable", "spacious"}},
}

// DefinitionFor returns the reviewed schema for a key. Unknown keys are
// constrained low-risk JSON preferences; they cannot claim safety authority.
func DefinitionFor(key string) Definition {
	if definition, ok := definitions[key]; ok {
		definition.AllowedEnums = append([]string(nil), definition.AllowedEnums...)
		return definition
	}
	return Definition{Key: key, Kind: ValueJSON, Risk: RiskLowPreference}
}

func ValidateValue(key, valueJSON string) error {
	if strings.TrimSpace(key) == "" || len(valueJSON) == 0 || len(valueJSON) > 65536 || !json.Valid([]byte(valueJSON)) || redaction.RedactString(valueJSON) != valueJSON {
		return fmt.Errorf("setting value must be bounded valid sanitized JSON")
	}
	definition := DefinitionFor(key)
	if definition.Kind != ValueJSON && strings.TrimSpace(valueJSON) == "null" {
		return fmt.Errorf("setting %q cannot be null", key)
	}
	switch definition.Kind {
	case ValueBoolean:
		var value bool
		if err := json.Unmarshal([]byte(valueJSON), &value); err != nil {
			return fmt.Errorf("setting %q requires a boolean", key)
		}
	case ValueInteger:
		var value int64
		if err := json.Unmarshal([]byte(valueJSON), &value); err != nil || value < definition.MinInteger || value > definition.MaxInteger {
			return fmt.Errorf("setting %q requires an integer in the reviewed range", key)
		}
	case ValueEnum:
		var value string
		if err := json.Unmarshal([]byte(valueJSON), &value); err != nil {
			return fmt.Errorf("setting %q requires an enumerated string", key)
		}
		for _, allowed := range definition.AllowedEnums {
			if value == allowed {
				return nil
			}
		}
		return fmt.Errorf("setting %q contains an unrecognized enum value", key)
	case ValueJSON:
		return nil
	default:
		return fmt.Errorf("setting %q has an unsupported value kind", key)
	}
	return nil
}

type Scope string

const (
	ScopeWorkspace          Scope = "workspace"
	ScopeControlPlane       Scope = "control_plane"
	ScopeEdgeHost           Scope = "edge_host"
	ScopeDevice             Scope = "device"
	ScopeAutomationAgent    Scope = "automation_agent"
	ScopeOperatorPreference Scope = "operator_preference"
)

type State string

const (
	Draft      State = "draft"
	Active     State = "active"
	Superseded State = "superseded"
	Retired    State = "retired"
)

type Setting struct {
	ID         SettingID
	Workspace  organizations.WorkspaceID
	Scope      Scope
	TargetID   string
	Key        string
	ValueJSON  string
	State      State
	CreatedAt  time.Time
	UpdatedAt  time.Time
	RowVersion uint64
}

// Change is an append-only record of a setting projection write. The value is
// safe because Setting.Validate is required before a change can be recorded.
type Change struct {
	ID         string
	Workspace  organizations.WorkspaceID
	SettingID  SettingID
	Scope      Scope
	TargetID   string
	Key        string
	ValueJSON  string
	State      State
	RowVersion uint64
	ActorType  string
	ActorID    string
	ChangedAt  time.Time
}

func (s Scope) Valid() bool {
	switch s {
	case ScopeWorkspace, ScopeControlPlane, ScopeEdgeHost, ScopeDevice, ScopeAutomationAgent, ScopeOperatorPreference:
		return true
	default:
		return false
	}
}

func (s Setting) Validate() error {
	if strings.TrimSpace(string(s.ID)) == "" || strings.TrimSpace(string(s.Workspace)) == "" || !s.Scope.Valid() || strings.TrimSpace(s.Key) == "" || !s.State.Valid() {
		return fmt.Errorf("setting identity, scope, key, or state is invalid")
	}
	if s.Scope == ScopeWorkspace && strings.TrimSpace(s.TargetID) != "" {
		return fmt.Errorf("workspace settings cannot have a target ID")
	}
	if s.Scope != ScopeWorkspace && strings.TrimSpace(s.TargetID) == "" {
		return fmt.Errorf("scoped settings require a target ID")
	}
	return ValidateValue(s.Key, s.ValueJSON)
}

func (s State) Valid() bool {
	switch s {
	case Draft, Active, Superseded, Retired:
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
		return to == Superseded || to == Retired
	case Superseded:
		return to == Retired
	case Retired:
		return false
	default:
		return false
	}
}

func Transition(from, to State) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("setting", string(from), string(to))
	}
	return nil
}
