// Package workflows owns immutable, typed workflow definitions. It validates
// definitions before a run can snapshot targets or dispatch an action.
package workflows

import (
	"encoding/json"
	"fmt"
	"strings"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/redaction"
)

type ID string
type VersionID string
type StepID string

type State string

const (
	StateDraft      State = "draft"
	StateValidated  State = "validated"
	StatePublished  State = "published"
	StateDeprecated State = "deprecated"
	StateRetired    State = "retired"
)

const (
	maxWorkflowNameBytes = 256
	maxWorkflowJSONBytes = 262144
	maxStepFieldBytes    = 512
)

// StepDefinition contains only declarative, semantic execution metadata. It
// deliberately has no coordinates, shell commands, credentials, or raw
// protocol payloads.
type StepDefinition struct {
	Target              action.SemanticTarget `json:"target"`
	TimeoutMillis       int64                 `json:"timeout_ms"`
	RequiresObservation bool                  `json:"requires_observation"`
	EvidenceRequired    bool                  `json:"evidence_required"`
	Postcondition       string                `json:"postcondition"`
}

type Step struct {
	ID         StepID
	Sequence   int
	Action     action.Kind
	Risk       action.RiskClass
	Retry      action.RetryClass
	Definition StepDefinition
}

type Version struct {
	ID         VersionID
	Workspace  organizations.WorkspaceID
	WorkflowID ID
	Version    int
	State      State
	Steps      []Step
}

type Workflow struct {
	ID        ID
	Workspace organizations.WorkspaceID
	Name      string
	State     State
}

func (s State) Valid() bool {
	switch s {
	case StateDraft, StateValidated, StatePublished, StateDeprecated, StateRetired:
		return true
	default:
		return false
	}
}

func CanTransition(from, to State) bool {
	switch from {
	case StateDraft:
		return to == StateValidated || to == StateRetired
	case StateValidated:
		return to == StateDraft || to == StatePublished || to == StateRetired
	case StatePublished:
		return to == StateDeprecated || to == StateRetired
	case StateDeprecated:
		return to == StateRetired
	default:
		return false
	}
}

func Transition(from, to State) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("workflow", string(from), string(to))
	}
	return nil
}

func (w Workflow) Validate() error {
	if strings.TrimSpace(string(w.ID)) == "" || strings.TrimSpace(string(w.Workspace)) == "" || strings.TrimSpace(w.Name) == "" {
		return fmt.Errorf("workflow identity is required")
	}
	if len(w.Name) > maxWorkflowNameBytes || !w.State.Valid() {
		return fmt.Errorf("workflow identity is invalid or unbounded")
	}
	return nil
}

func (d StepDefinition) Validate(spec action.Specification) error {
	if d.TimeoutMillis <= 0 || d.TimeoutMillis > 5*60*1000 {
		return fmt.Errorf("step timeout must be between one millisecond and five minutes")
	}
	if spec.RequiresTarget && d.Target.Empty() {
		return fmt.Errorf("action %q requires a semantic target", spec.Kind)
	}
	if spec.RequiresObservation && !d.RequiresObservation {
		return fmt.Errorf("action %q requires a fresh observation", spec.Kind)
	}
	if spec.EvidenceRequired && !d.EvidenceRequired {
		return fmt.Errorf("action %q requires evidence", spec.Kind)
	}
	if spec.Mutating && strings.TrimSpace(d.Postcondition) == "" {
		return fmt.Errorf("mutating action %q requires a postcondition", spec.Kind)
	}
	for _, value := range []string{d.Target.ResourceID, d.Target.AccessibilityLabel, d.Target.StableText, d.Target.ContextFingerprint, d.Postcondition} {
		if len(value) > maxStepFieldBytes || redaction.RedactString(value) != value {
			return fmt.Errorf("step metadata is invalid or sensitive")
		}
	}
	return nil
}

func (s Step) Validate() error {
	if strings.TrimSpace(string(s.ID)) == "" || s.Sequence < 0 {
		return fmt.Errorf("workflow step identity and sequence are required")
	}
	spec, ok := action.Lookup(s.Action)
	if !ok {
		return fmt.Errorf("workflow step action %q is not allow-listed", s.Action)
	}
	if s.Risk != spec.Risk || s.Retry != spec.Retry {
		return fmt.Errorf("workflow step safety metadata does not match action catalog")
	}
	return s.Definition.Validate(spec)
}

func (v Version) Validate() error {
	if strings.TrimSpace(string(v.ID)) == "" || strings.TrimSpace(string(v.Workspace)) == "" || strings.TrimSpace(string(v.WorkflowID)) == "" || v.Version <= 0 || !v.State.Valid() {
		return fmt.Errorf("workflow version identity is invalid")
	}
	if len(v.Steps) == 0 {
		return fmt.Errorf("workflow version must contain at least one step")
	}
	for index, step := range v.Steps {
		if step.Sequence != index {
			return fmt.Errorf("workflow step sequence must be contiguous from zero")
		}
		if err := step.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// DefinitionJSON is the immutable, bounded representation persisted with a
// workflow version. It is generated from typed steps and cannot accept an
// arbitrary caller-owned JSON blob.
func (v Version) DefinitionJSON() (string, error) {
	if err := v.Validate(); err != nil {
		return "", err
	}
	value := struct {
		Version int    `json:"version"`
		Steps   []Step `json:"steps"`
	}{Version: v.Version, Steps: v.Steps}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode workflow definition: %w", err)
	}
	if len(encoded) > maxWorkflowJSONBytes || redaction.RedactString(string(encoded)) != string(encoded) {
		return "", fmt.Errorf("workflow definition is oversized or sensitive")
	}
	return string(encoded), nil
}
