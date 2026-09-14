// Package skills owns reviewed, immutable deterministic-skill promotion state.
// A skill is a typed workflow package, never an arbitrary script or downloaded
// executable.
package skills

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/redaction"
	"drift.local/drift-next/internal/recordings"
	"drift.local/drift-next/internal/workflows"
)

type SkillID string
type SkillVersionID string

type State string

const (
	Draft      State = "draft"
	Validated  State = "validated"
	Published  State = "published"
	Deprecated State = "deprecated"
	Retired    State = "retired"
)

type TrustState string

const (
	TrustUnreviewed TrustState = "unreviewed"
	TrustReviewed   TrustState = "reviewed"
	TrustApproved   TrustState = "approved"
	TrustRevoked    TrustState = "revoked"
)

const (
	maxSkillNameBytes       = 256
	maxSkillMetadataBytes   = 262144
	maxSkillFixtureCount    = 1024
	maxCompatibilityEntries = 128
)

type Compatibility struct {
	PackageNames    []string `json:"package_names,omitempty"`
	ActivityNames   []string `json:"activity_names,omitempty"`
	AppVersions     []string `json:"app_versions,omitempty"`
	CoordinateSpace []string `json:"coordinate_spaces,omitempty"`
}

type Manifest struct {
	Compatibility         Compatibility       `json:"compatibility"`
	RequestedCapabilities []action.Capability `json:"requested_capabilities"`
	Fixtures              []string            `json:"fixtures"`
	Risk                  action.RiskClass    `json:"risk_class"`
	Retry                 action.RetryClass   `json:"retry_class"`
}

type Skill struct {
	ID        SkillID
	Workspace organizations.WorkspaceID
	Name      string
	State     State
	CreatedAt time.Time
	UpdatedAt time.Time
}

type SkillVersion struct {
	ID                     SkillVersionID
	Workspace              organizations.WorkspaceID
	SkillID                SkillID
	Version                int
	State                  State
	Trust                  TrustState
	Manifest               Manifest
	Steps                  []workflows.Step
	SourceRecordingSession recordings.RecordingSessionID
	RollbackOf             SkillVersionID
	CreatedAt              time.Time
	ReviewedAt             *time.Time
	ReviewerID             string
}

type Promotion struct {
	ID           string
	Workspace    organizations.WorkspaceID
	SkillVersion SkillVersionID
	From         State
	To           State
	ActorID      string
	Reason       string
	OccurredAt   time.Time
}

type BrainKnowledge struct {
	ID                 string
	Workspace          organizations.WorkspaceID
	Key                string
	Version            int
	State              State
	KnowledgeJSON      string
	SourceSkillVersion SkillVersionID
}

func (s State) Valid() bool {
	switch s {
	case Draft, Validated, Published, Deprecated, Retired:
		return true
	default:
		return false
	}
}

func (s TrustState) Valid() bool {
	switch s {
	case TrustUnreviewed, TrustReviewed, TrustApproved, TrustRevoked:
		return true
	default:
		return false
	}
}

func CanTransition(from, to State) bool {
	switch from {
	case Draft:
		return to == Validated || to == Retired
	case Validated:
		return to == Draft || to == Published || to == Retired
	case Published:
		return to == Deprecated || to == Retired
	case Deprecated:
		return to == Retired
	case Retired:
		return false
	default:
		return false
	}
}

func Transition(from, to State) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("skill", string(from), string(to))
	}
	return nil
}

func (c Compatibility) Validate() error {
	for _, values := range [][]string{c.PackageNames, c.ActivityNames, c.AppVersions, c.CoordinateSpace} {
		if len(values) > maxCompatibilityEntries {
			return fmt.Errorf("skill compatibility has too many entries")
		}
		for _, value := range values {
			if strings.TrimSpace(value) == "" || len(value) > 256 || redaction.RedactString(value) != value {
				return fmt.Errorf("skill compatibility entry is invalid or sensitive")
			}
		}
	}
	return nil
}

func (m Manifest) Validate() error {
	if err := m.Compatibility.Validate(); err != nil {
		return err
	}
	if !validRisk(m.Risk) || !validRetry(m.Retry) {
		return fmt.Errorf("skill safety metadata is invalid")
	}
	if len(m.RequestedCapabilities) > 64 || len(m.Fixtures) > maxSkillFixtureCount {
		return fmt.Errorf("skill manifest is too large")
	}
	seenCapabilities := make(map[action.Capability]struct{}, len(m.RequestedCapabilities))
	for _, capability := range m.RequestedCapabilities {
		if strings.TrimSpace(string(capability)) == "" || len(capability) > 128 || !capability.Valid() {
			return fmt.Errorf("skill capability is invalid")
		}
		if _, ok := seenCapabilities[capability]; ok {
			return fmt.Errorf("skill capabilities must be unique")
		}
		seenCapabilities[capability] = struct{}{}
	}
	for _, fixture := range m.Fixtures {
		if strings.TrimSpace(fixture) == "" || len(fixture) > 256 || redaction.RedactString(fixture) != fixture {
			return fmt.Errorf("skill fixture is invalid or sensitive")
		}
	}
	return nil
}

func (s Skill) Validate() error {
	if strings.TrimSpace(string(s.ID)) == "" || strings.TrimSpace(string(s.Workspace)) == "" || strings.TrimSpace(s.Name) == "" || len(s.Name) > maxSkillNameBytes || !s.State.Valid() || s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() {
		return fmt.Errorf("skill identity or state is invalid")
	}
	if s.UpdatedAt.Before(s.CreatedAt) {
		return fmt.Errorf("skill update precedes creation")
	}
	return nil
}

func (v SkillVersion) WorkflowVersion() workflows.Version {
	return workflows.Version{ID: workflows.VersionID(v.ID), Workspace: v.Workspace, WorkflowID: workflows.ID(v.SkillID), Version: v.Version, State: workflows.State(v.State), Steps: append([]workflows.Step(nil), v.Steps...)}
}

func (v SkillVersion) Validate() error {
	if strings.TrimSpace(string(v.ID)) == "" || strings.TrimSpace(string(v.Workspace)) == "" || strings.TrimSpace(string(v.SkillID)) == "" || v.Version <= 0 || !v.State.Valid() || !v.Trust.Valid() || v.CreatedAt.IsZero() || len(v.ReviewerID) > 256 || len(v.SourceRecordingSession) > 256 || len(v.RollbackOf) > 256 {
		return fmt.Errorf("skill version identity or state is invalid")
	}
	if err := v.Manifest.Validate(); err != nil {
		return err
	}
	if err := v.WorkflowVersion().Validate(); err != nil {
		return fmt.Errorf("skill steps are invalid: %w", err)
	}
	if v.State == Published && v.Trust != TrustApproved {
		return fmt.Errorf("published skills require approved trust")
	}
	if v.State == Validated && v.Trust != TrustReviewed && v.Trust != TrustApproved {
		return fmt.Errorf("validated skills require review")
	}
	if v.ReviewedAt != nil && v.ReviewedAt.Before(v.CreatedAt) {
		return fmt.Errorf("skill review precedes creation")
	}
	return nil
}

func (v SkillVersion) DefinitionJSON() (string, error) {
	if err := v.Validate(); err != nil {
		return "", err
	}
	definition, err := v.WorkflowVersion().DefinitionJSON()
	if err != nil {
		return "", err
	}
	if len(definition) > maxSkillMetadataBytes || redaction.RedactString(definition) != definition {
		return "", fmt.Errorf("skill definition is oversized or sensitive")
	}
	return definition, nil
}

func (v SkillVersion) ManifestJSON() (string, error) {
	if err := v.Manifest.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(v.Manifest)
	if err != nil {
		return "", fmt.Errorf("encode skill manifest: %w", err)
	}
	if len(encoded) > maxSkillMetadataBytes || redaction.RedactString(string(encoded)) != string(encoded) {
		return "", fmt.Errorf("skill manifest is oversized or sensitive")
	}
	return string(encoded), nil
}

func (k BrainKnowledge) Validate() error {
	if strings.TrimSpace(k.ID) == "" || strings.TrimSpace(string(k.Workspace)) == "" || strings.TrimSpace(k.Key) == "" || k.Version <= 0 || !k.State.Valid() || strings.TrimSpace(k.KnowledgeJSON) == "" || len(k.KnowledgeJSON) > 131072 || redaction.RedactString(k.KnowledgeJSON) != k.KnowledgeJSON {
		return fmt.Errorf("shared-brain knowledge is invalid or sensitive")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(k.KnowledgeJSON), &object); err != nil || object == nil {
		return fmt.Errorf("shared-brain knowledge must be a JSON object")
	}
	return nil
}

func unionStrings(values ...[]string) []string {
	set := make(map[string]struct{})
	for _, group := range values {
		for _, value := range group {
			if value != "" {
				set[value] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func maxRisk(left, right action.RiskClass) action.RiskClass {
	rank := map[action.RiskClass]int{action.RiskLow: 1, action.RiskMedium: 2, action.RiskHigh: 3, action.RiskIrreversible: 4}
	if rank[right] > rank[left] {
		return right
	}
	return left
}

func strictRetry(left, right action.RetryClass) action.RetryClass {
	rank := map[action.RetryClass]int{action.RetrySafe: 1, action.RetryAfterObservation: 2, action.RetryNeverBlind: 3}
	if rank[right] > rank[left] {
		return right
	}
	return left
}

func validRisk(value action.RiskClass) bool {
	return value == action.RiskLow || value == action.RiskMedium || value == action.RiskHigh || value == action.RiskIrreversible
}

func validRetry(value action.RetryClass) bool {
	return value == action.RetrySafe || value == action.RetryAfterObservation || value == action.RetryNeverBlind
}
