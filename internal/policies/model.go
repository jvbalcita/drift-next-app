// Package policies owns typed safety policy versions and their decisions.
package policies

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/redaction"
)

type PolicyID string
type PolicyDecisionID string

type State string

const (
	Draft      State = "draft"
	Active     State = "active"
	Superseded State = "superseded"
	Retired    State = "retired"
)

type Decision string

const (
	Allow        Decision = "allow"
	Deny         Decision = "deny"
	Inconclusive Decision = "inconclusive"
)

func (d Decision) Valid() bool {
	switch d {
	case Allow, Deny, Inconclusive:
		return true
	default:
		return false
	}
}

type Policy struct {
	ID        PolicyID
	Workspace organizations.WorkspaceID
	Name      string
	Version   int
	RuleJSON  string
	State     State
}

type PolicyDecision struct {
	ID           PolicyDecisionID
	Workspace    organizations.WorkspaceID
	PolicyID     PolicyID
	ResourceType string
	ResourceID   string
	Action       string
	Decision     Decision
	ReasonCode   string
	DecidedAt    time.Time
}

func (p Policy) Validate() error {
	if strings.TrimSpace(string(p.ID)) == "" || strings.TrimSpace(string(p.Workspace)) == "" || strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("policy identity and name are required")
	}
	if p.Version <= 0 || !p.State.Valid() {
		return fmt.Errorf("policy version or state is invalid")
	}
	if len(p.RuleJSON) == 0 || len(p.RuleJSON) > 131072 || !json.Valid([]byte(p.RuleJSON)) {
		return fmt.Errorf("policy rule must be bounded valid JSON")
	}
	if redaction.RedactString(p.RuleJSON) != p.RuleJSON {
		return fmt.Errorf("policy rule contains sensitive material")
	}
	return nil
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
		return domain.InvalidTransition("policy", string(from), string(to))
	}
	return nil
}
