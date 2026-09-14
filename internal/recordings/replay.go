package recordings

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adapter"
	"drift.local/drift-next/internal/workflows"
)

type ResolutionMethod string

const (
	ResolutionResourceID    ResolutionMethod = "resource_id"
	ResolutionAccessibility ResolutionMethod = "accessibility"
	ResolutionStableText    ResolutionMethod = "stable_text"
	ResolutionContext       ResolutionMethod = "context"
	ResolutionVision        ResolutionMethod = "vision"
	ResolutionCoordinate    ResolutionMethod = "coordinate_fallback"
	ResolutionNone          ResolutionMethod = "none"
)

type ReplayPolicy struct {
	ExpectedPackageName     string
	ExpectedActivityName    string
	ExpectedAppVersion      string
	ExpectedCoordinateSpace string
	AllowVision             bool
	AllowCoordinateFallback bool
	OperatorConfirmed       bool
}

type ReplayAuthorization struct {
	Workspace       string
	DeviceID        string
	LeaseID         string
	HolderID        string
	FencingToken    uint64
	ApprovalGranted bool
	Capabilities    []action.Capability
	Policy          ReplayPolicy
}

type VisionResolver interface {
	Resolve(context.Context, adapter.Observation, action.SemanticTarget) (adapter.TargetNode, error)
}

type ReplayResult struct {
	EventID          RecordingEventID
	Sequence         int
	Resolution       ResolutionMethod
	Outcome          action.Outcome
	Postcondition    action.PostconditionState
	Failure          domain.FailureClass
	ObservationToken string
	AfterToken       string
	CleanupSucceeded bool
}

// Replay executes a published typed workflow through the narrow fake adapter
// boundary. It resolves targets from the current observation for every step;
// it never reuses a recorded coordinate or UI node as authority.
func Replay(ctx context.Context, version workflows.Version, events []InteractionEvent, device adapter.Adapter, authorization ReplayAuthorization, vision VisionResolver) ([]ReplayResult, error) {
	if ctx == nil || device == nil {
		return nil, fmt.Errorf("context and replay adapter are required")
	}
	if err := version.Validate(); err != nil {
		return nil, fmt.Errorf("workflow skill is invalid: %w", err)
	}
	if version.State != workflows.StatePublished {
		return nil, fmt.Errorf("only a published workflow skill can replay")
	}
	if err := validateReplayAuthorization(authorization); err != nil {
		return nil, err
	}
	if string(version.Workspace) != authorization.Workspace {
		return nil, fmt.Errorf("replay workflow and authorization workspaces must match")
	}
	byID := make(map[RecordingEventID]InteractionEvent, len(events))
	for _, event := range events {
		if err := event.Validate(); err != nil {
			return nil, fmt.Errorf("replay event is invalid: %w", err)
		}
		if event.Review != ReviewApproved || event.Sensitive {
			return nil, fmt.Errorf("only approved non-sensitive events can replay")
		}
		if string(event.Workspace) != authorization.Workspace || event.SessionID == "" {
			return nil, fmt.Errorf("replay event is outside the authorized workspace")
		}
		if _, exists := byID[event.ID]; exists {
			return nil, fmt.Errorf("replay contains duplicate recording event %q", event.ID)
		}
		byID[event.ID] = CloneEvent(event)
	}
	results := make([]ReplayResult, 0, len(version.Steps))
	for _, step := range version.Steps {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		event, ok := byID[RecordingEventID(step.ID)]
		if !ok {
			return results, fmt.Errorf("replay step %q has no recording event", step.ID)
		}
		if !replayStepMatchesEvent(step, event) {
			return results, fmt.Errorf("replay step %q does not match its reviewed recording event", step.ID)
		}
		result := ReplayResult{EventID: event.ID, Sequence: step.Sequence, Outcome: action.OutcomeFailed, Postcondition: action.PostconditionUnknown, CleanupSucceeded: false}
		current, observeErr := device.Observe(ctx)
		if observeErr != nil {
			result.Failure = domain.FailureObservation
			results = append(results, result)
			return results, nil
		}
		result.ObservationToken = current.Token
		if failure := checkReplayIdentity(current, event, authorization.Policy); failure != "" {
			result.Failure = failure
			results = append(results, result)
			return results, nil
		}
		resolved, method, fallback, resolveErr := resolveReplayTarget(ctx, current, event.Action, step, authorization.Policy, vision)
		if resolveErr.class != "" {
			result.Failure = resolveErr.class
			results = append(results, result)
			return results, nil
		}
		result.Resolution = method
		intent := action.Intent{ID: string(event.ID), Workspace: authorization.Workspace, DeviceID: authorization.DeviceID, LeaseID: authorization.LeaseID, HolderID: authorization.HolderID, FencingToken: authorization.FencingToken, Kind: step.Action, Target: resolved, TextValue: step.Definition.TextValue, ValueLength: step.Definition.ValueLength, KeyCode: step.Definition.KeyCode, IdempotencyKey: fmt.Sprintf("replay:%s:%d", version.ID, step.Sequence), ObservationToken: current.Token, InvocationSurface: action.SurfaceReplay, Capabilities: append([]action.Capability(nil), authorization.Capabilities...), ApprovalGranted: authorization.ApprovalGranted, Timeout: time.Duration(step.Definition.TimeoutMillis) * time.Millisecond, CoordinateFallback: fallback}
		if step.Definition.Gesture != nil {
			intent.Gesture = &action.GesturePath{Points: append([]action.Coordinate(nil), step.Definition.Gesture.Points...), DurationMs: step.Definition.Gesture.DurationMs}
		}
		if err := intent.Validate(); err != nil {
			result.Failure = domain.FailurePolicyDenied
			results = append(results, result)
			return results, nil
		}
		execution, executeErr := device.Execute(ctx, intent)
		result.CleanupSucceeded = device.Cleanup(ctx, intent) == nil
		if executeErr != nil {
			if errors.Is(executeErr, context.Canceled) {
				return results, executeErr
			}
			var executionError *adapter.ExecutionError
			if errors.As(executeErr, &executionError) && executionError.Dispatched {
				result.Outcome = action.OutcomeIndeterminate
				result.Failure = domain.FailureIndeterminate
			} else {
				result.Outcome = action.OutcomeFailed
				result.Failure = domain.FailureTransport
			}
			results = append(results, result)
			return results, nil
		}
		result.Outcome = execution.Outcome
		result.Postcondition = execution.Postcondition
		if result.Outcome == "" {
			result.Outcome = action.OutcomeFailed
		}
		if result.Outcome == action.OutcomeIndeterminate || execution.TransportLost {
			result.Outcome = action.OutcomeIndeterminate
			result.Failure = domain.FailureIndeterminate
		}
		if result.Outcome == action.OutcomeVerified && result.Postcondition != action.PostconditionPassed {
			result.Outcome = action.OutcomeFailed
			result.Failure = domain.FailurePostcondition
		}
		if result.Outcome == action.OutcomeVerified && !completeReplayCapture(event.After) {
			result.Outcome = action.OutcomeFailed
			result.Failure = domain.FailureObservation
		}
		if !result.CleanupSucceeded {
			result.Outcome = action.OutcomeFailed
			result.Failure = domain.FailureCleanupFailed
		}
		if step.Definition.RequiresObservation {
			after, afterErr := device.Observe(ctx)
			if afterErr != nil {
				if result.Outcome == action.OutcomeIndeterminate {
					result.Failure = domain.FailureIndeterminate
				} else {
					result.Outcome = action.OutcomeFailed
					result.Failure = domain.FailureObservation
				}
			} else {
				result.AfterToken = after.Token
				if after.Token == current.Token && result.Outcome != action.OutcomeIndeterminate {
					result.Outcome = action.OutcomeFailed
					result.Failure = domain.FailureStaleObservation
				}
			}
		}
		results = append(results, result)
		if result.Outcome != action.OutcomeVerified || result.Failure != "" {
			return results, nil
		}
	}
	return results, nil
}

type resolveError struct {
	class domain.FailureClass
}

func (e resolveError) Error() string { return string(e.class) }

func validateReplayAuthorization(authorization ReplayAuthorization) error {
	if strings.TrimSpace(authorization.Workspace) == "" || strings.TrimSpace(authorization.DeviceID) == "" || strings.TrimSpace(authorization.LeaseID) == "" || strings.TrimSpace(authorization.HolderID) == "" || authorization.FencingToken == 0 || !authorization.ApprovalGranted {
		return fmt.Errorf("replay requires an approved workspace lease and fencing token")
	}
	return nil
}

func replayStepMatchesEvent(step workflows.Step, event InteractionEvent) bool {
	if event.Sequence != step.Sequence || event.Action.Kind != step.Action || event.Action.Target.Semantic != step.Definition.Target || event.Action.DisplayValue != step.Definition.TextValue || event.Action.ValueLength != step.Definition.ValueLength || event.Action.KeyCode != step.Definition.KeyCode {
		return false
	}
	if step.Definition.Gesture == nil || event.Action.Gesture == nil {
		return step.Definition.Gesture == nil && event.Action.Gesture == nil
	}
	if step.Definition.Gesture.DurationMs != event.Action.Gesture.DurationMs || len(step.Definition.Gesture.Points) != len(event.Action.Gesture.Points) {
		return false
	}
	for index, point := range step.Definition.Gesture.Points {
		if point != event.Action.Gesture.Points[index] {
			return false
		}
	}
	return true
}

func checkReplayIdentity(current adapter.Observation, event InteractionEvent, policy ReplayPolicy) domain.FailureClass {
	expectedPackage := policy.ExpectedPackageName
	expectedActivity := policy.ExpectedActivityName
	expectedVersion := policy.ExpectedAppVersion
	if event.Before != nil {
		if expectedPackage == "" {
			expectedPackage = event.Before.PackageName
		}
		if expectedActivity == "" {
			expectedActivity = event.Before.ActivityName
		}
		if expectedVersion == "" {
			expectedVersion = event.Before.AppVersion
		}
	}
	if expectedPackage != "" && current.PackageName != expectedPackage || expectedActivity != "" && current.ActivityName != expectedActivity || expectedVersion != "" && current.AppVersion != expectedVersion {
		return domain.FailureUnknownScreen
	}
	if policy.ExpectedCoordinateSpace != "" && current.CoordinateSpace != policy.ExpectedCoordinateSpace {
		return domain.FailureStaleObservation
	}
	return ""
}

func resolveReplayTarget(ctx context.Context, current adapter.Observation, recorded LogicalAction, step workflows.Step, policy ReplayPolicy, vision VisionResolver) (action.SemanticTarget, ResolutionMethod, *action.CoordinateFallback, resolveError) {
	spec, ok := action.Lookup(step.Action)
	if !ok {
		return action.SemanticTarget{}, ResolutionNone, nil, resolveError{class: domain.FailureUnknownScreen}
	}
	target := step.Definition.Target
	if !spec.RequiresTarget {
		return target, ResolutionNone, nil, resolveError{}
	}
	selectors := []struct {
		value  string
		method ResolutionMethod
		match  func(adapter.TargetNode, string) bool
	}{
		{target.ResourceID, ResolutionResourceID, func(node adapter.TargetNode, value string) bool { return node.ResourceID == value }},
		{target.AccessibilityLabel, ResolutionAccessibility, func(node adapter.TargetNode, value string) bool { return node.AccessibilityLabel == value }},
		{target.StableText, ResolutionStableText, func(node adapter.TargetNode, value string) bool { return node.StableText == value }},
		{target.ContextFingerprint, ResolutionContext, func(node adapter.TargetNode, value string) bool { return node.ContextFingerprint == value }},
	}
	for _, selector := range selectors {
		if selector.value == "" {
			continue
		}
		matches := make([]adapter.TargetNode, 0, 1)
		for _, node := range current.Nodes {
			if selector.match(node, selector.value) {
				matches = append(matches, node)
			}
		}
		if len(matches) > 1 {
			return action.SemanticTarget{}, selector.method, nil, resolveError{class: domain.FailureAmbiguousTarget}
		}
		if len(matches) == 1 {
			if !matches[0].Actionable || !matches[0].Enabled {
				return action.SemanticTarget{}, selector.method, nil, resolveError{class: domain.FailureAmbiguousTarget}
			}
			return nodeSemanticTarget(matches[0]), selector.method, nil, resolveError{}
		}
	}
	if policy.AllowVision && vision != nil {
		node, err := vision.Resolve(ctx, current, target)
		if err == nil {
			if !node.Actionable || !node.Enabled {
				return action.SemanticTarget{}, ResolutionVision, nil, resolveError{class: domain.FailureAmbiguousTarget}
			}
			return nodeSemanticTarget(node), ResolutionVision, nil, resolveError{}
		}
	}
	fallback, ok := coordinateFallbackFor(recorded, current, policy)
	if ok {
		return action.SemanticTarget{}, ResolutionCoordinate, fallback, resolveError{}
	}
	return action.SemanticTarget{}, ResolutionNone, nil, resolveError{class: domain.FailureUnknownScreen}
}

func nodeSemanticTarget(node adapter.TargetNode) action.SemanticTarget {
	return action.SemanticTarget{ResourceID: node.ResourceID, AccessibilityLabel: node.AccessibilityLabel, StableText: node.StableText, ContextFingerprint: node.ContextFingerprint}
}

func coordinateFallbackFor(recorded LogicalAction, current adapter.Observation, policy ReplayPolicy) (*action.CoordinateFallback, bool) {
	if !policy.AllowCoordinateFallback || !policy.OperatorConfirmed || current.Partial {
		return nil, false
	}
	var start action.Coordinate
	var end *action.Coordinate
	duration := time.Duration(0)
	if recorded.Gesture != nil && len(recorded.Gesture.Points) >= 2 {
		start = recorded.Gesture.Points[0]
		point := recorded.Gesture.Points[len(recorded.Gesture.Points)-1]
		end = &point
		duration = time.Duration(recorded.Gesture.DurationMs) * time.Millisecond
	} else if recorded.Coordinate != nil {
		start = *recorded.Coordinate
	} else {
		return nil, false
	}
	if current.CoordinateSpace == "" || start.Space != current.CoordinateSpace || (policy.ExpectedCoordinateSpace != "" && start.Space != policy.ExpectedCoordinateSpace) {
		return nil, false
	}
	width, height := current.DisplayWidth, current.DisplayHeight
	if width <= 0 || height <= 0 || !inBounds(start, width, height) || end != nil && !inBounds(*end, width, height) {
		return nil, false
	}
	result := &action.CoordinateFallback{Start: start, End: end, Duration: duration, Confirmed: true}
	if recorded.Gesture != nil {
		result.Path = append([]action.Coordinate(nil), recorded.Gesture.Points...)
	}
	return result, true
}

func inBounds(point action.Coordinate, width, height int) bool {
	if point.X < 0 || point.Y < 0 {
		return false
	}
	if width > 0 && point.X >= width {
		return false
	}
	if height > 0 && point.Y >= height {
		return false
	}
	return true
}

func completeReplayCapture(capture *Capture) bool {
	return capture != nil && capture.Status == CaptureComplete && capture.Sanitization != Unsanitizable && capture.ErrorClass == ""
}
