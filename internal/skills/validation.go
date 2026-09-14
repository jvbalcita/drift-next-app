package skills

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/recordings"
	"drift.local/drift-next/internal/workflows"
)

type CompileRequest struct {
	ID                     SkillVersionID
	Workspace              string
	SkillID                SkillID
	Version                int
	SourceRecordingSession recordings.RecordingSessionID
	Now                    time.Time
}

// CompileRecording turns reviewed logical events into a typed draft skill.
// It intentionally does not publish or execute the result.
func CompileRecording(request CompileRequest, session recordings.Session, events []recordings.InteractionEvent) (SkillVersion, error) {
	result := SkillVersion{ID: request.ID, Workspace: organizationsID(request.Workspace), SkillID: request.SkillID, Version: request.Version, State: Draft, Trust: TrustUnreviewed, SourceRecordingSession: request.SourceRecordingSession, CreatedAt: request.Now.UTC()}
	if result.SourceRecordingSession == "" {
		result.SourceRecordingSession = session.ID
	}
	if err := validateCompileRequest(request, session); err != nil {
		return SkillVersion{}, err
	}
	if result.Workspace == "" {
		result.Workspace = session.Workspace
	}
	if result.Workspace != session.Workspace {
		return SkillVersion{}, fmt.Errorf("skill and recording workspaces must match")
	}
	if len(events) == 0 {
		return SkillVersion{}, fmt.Errorf("recording has no events")
	}
	ordered := append([]recordings.InteractionEvent(nil), events...)
	sort.SliceStable(ordered, func(left, right int) bool { return ordered[left].Sequence < ordered[right].Sequence })
	manifest := Manifest{Risk: action.RiskLow, Retry: action.RetrySafe}
	steps := make([]workflowsStep, 0, len(ordered))
	for index, event := range ordered {
		if event.Sequence != index {
			return SkillVersion{}, fmt.Errorf("recording event sequence is not contiguous")
		}
		if event.SessionID != session.ID || event.Workspace != session.Workspace {
			return SkillVersion{}, fmt.Errorf("recording event is outside the source session")
		}
		if err := event.Validate(); err != nil {
			return SkillVersion{}, fmt.Errorf("recording event %q is invalid: %w", event.ID, err)
		}
		if event.Review != recordings.ReviewApproved {
			return SkillVersion{}, fmt.Errorf("recording event %q is not approved", event.ID)
		}
		if event.Sensitive || event.Action.Sensitivity != recordings.SensitivityNone {
			return SkillVersion{}, fmt.Errorf("sensitive event %q cannot become an executable skill", event.ID)
		}
		spec, ok := action.Lookup(event.Action.Kind)
		if !ok {
			return SkillVersion{}, fmt.Errorf("recording event %q uses an unsupported action", event.ID)
		}
		if spec.RequiresTarget {
			if err := action.ValidateSemanticTarget(event.Action.Target.Semantic, event.Action.Target.CandidateCount, event.Action.Target.Actionable, event.Action.Target.Enabled); err != nil {
				return SkillVersion{}, fmt.Errorf("recording event %q target is not promotable: %w", event.ID, err)
			}
		}
		if spec.Risk == action.RiskIrreversible && !event.Action.IrreversibleConfirmed {
			return SkillVersion{}, fmt.Errorf("irreversible event %q requires confirmation", event.ID)
		}
		if spec.RequiresObservation && !completeCapture(event.Before) {
			return SkillVersion{}, fmt.Errorf("recording event %q lacks a complete BEFORE capture", event.ID)
		}
		if spec.Mutating && !completeCapture(event.After) {
			return SkillVersion{}, fmt.Errorf("recording event %q lacks a complete AFTER capture", event.ID)
		}
		if spec.EvidenceRequired && !hasEvidence(event) {
			return SkillVersion{}, fmt.Errorf("recording event %q lacks reviewable evidence", event.ID)
		}
		if event.Action.Kind == action.TextInput && event.Action.DisplayValue == "" {
			return SkillVersion{}, fmt.Errorf("text input event %q has no safe value", event.ID)
		}
		step := normalizeStep(event, spec)
		steps = append(steps, step)
		manifest.Risk = maxRisk(manifest.Risk, spec.Risk)
		manifest.Retry = strictRetry(manifest.Retry, spec.Retry)
		manifest.RequestedCapabilities = unionCapabilities(manifest.RequestedCapabilities, spec.RequiredCapabilities)
		manifest.Compatibility = addCompatibility(manifest.Compatibility, event)
		manifest.Fixtures = appendFixtures(manifest.Fixtures, event)
	}
	result.Steps = make([]workflows.Step, len(steps))
	copy(result.Steps, steps)
	result.Manifest = manifest
	if err := result.Validate(); err != nil {
		return SkillVersion{}, fmt.Errorf("compiled skill is invalid: %w", err)
	}
	return result, nil
}

// workflowsStep is an alias-like local name that keeps compile construction
// readable without exposing a second step vocabulary.
type workflowsStep = workflows.Step

func validateCompileRequest(request CompileRequest, session recordings.Session) error {
	if strings.TrimSpace(string(request.ID)) == "" || strings.TrimSpace(request.Workspace) == "" || strings.TrimSpace(string(request.SkillID)) == "" || request.Version <= 0 || request.Now.IsZero() {
		return fmt.Errorf("skill compilation identity and time are required")
	}
	if session.State != recordings.SessionCompleted || session.DeletedAt != nil {
		return fmt.Errorf("only an undeleted completed recording can be compiled")
	}
	if request.SourceRecordingSession != "" && request.SourceRecordingSession != session.ID {
		return fmt.Errorf("skill source recording does not match session")
	}
	return nil
}

func completeCapture(capture *recordings.Capture) bool {
	return capture != nil && capture.Status == recordings.CaptureComplete && capture.Sanitization != recordings.Unsanitizable && capture.ErrorClass == ""
}

func normalizeStep(event recordings.InteractionEvent, spec action.Specification) workflows.Step {
	definition := workflows.StepDefinition{Target: event.Action.Target.Semantic, TimeoutMillis: event.Action.TimeoutMillis, RequiresObservation: spec.RequiresObservation, EvidenceRequired: spec.EvidenceRequired, Postcondition: event.Action.Postcondition, TextValue: event.Action.DisplayValue, ValueLength: event.Action.ValueLength, KeyCode: event.Action.KeyCode}
	if event.Action.Gesture != nil {
		definition.Gesture = &action.GesturePath{Points: append([]action.Coordinate(nil), event.Action.Gesture.Points...), DurationMs: event.Action.Gesture.DurationMs}
	}
	return workflows.Step{ID: workflows.StepID(event.ID), Sequence: event.Sequence, Action: event.Action.Kind, Risk: spec.Risk, Retry: spec.Retry, Definition: definition}
}

func organizationsID(value string) (result organizations.WorkspaceID) {
	return organizations.WorkspaceID(value)
}

func unionCapabilities(existing, additions []action.Capability) []action.Capability {
	set := make(map[action.Capability]struct{}, len(existing)+len(additions))
	for _, value := range append(append([]action.Capability(nil), existing...), additions...) {
		set[value] = struct{}{}
	}
	result := make([]action.Capability, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return result
}

func addCompatibility(existing Compatibility, event recordings.InteractionEvent) Compatibility {
	addCapture := func(capture *recordings.Capture) {
		if capture == nil {
			return
		}
		existing.PackageNames = unionString(existing.PackageNames, capture.PackageName)
		existing.ActivityNames = unionString(existing.ActivityNames, capture.ActivityName)
		existing.AppVersions = unionString(existing.AppVersions, capture.AppVersion)
		existing.CoordinateSpace = unionString(existing.CoordinateSpace, capture.CoordinateSpace)
	}
	addCapture(event.Before)
	addCapture(event.After)
	return existing
}

func unionString(values []string, value string) []string {
	if value == "" {
		return values
	}
	return unionStrings(values, []string{value})
}

func appendFixtures(existing []string, event recordings.InteractionEvent) []string {
	result := append([]string(nil), existing...)
	for _, reference := range allEvidence(event) {
		if reference.Omitted || reference.ContentHash == "" {
			continue
		}
		result = unionString(result, reference.ContentHash)
	}
	return result
}

func allEvidence(event recordings.InteractionEvent) []recordings.EvidenceReference {
	result := append([]recordings.EvidenceReference(nil), event.Evidence...)
	if event.Before != nil {
		result = append(result, event.Before.Evidence...)
	}
	if event.After != nil {
		result = append(result, event.After.Evidence...)
	}
	return result
}

func hasEvidence(event recordings.InteractionEvent) bool {
	for _, reference := range allEvidence(event) {
		if !reference.Omitted && reference.Validate() == nil {
			return true
		}
	}
	return false
}
