package recordings_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adapter"
	"drift.local/drift-next/internal/recordings"
	"drift.local/drift-next/internal/workflows"
)

func TestReplayResolvesCurrentSemanticTargetAndRequiresFreshAfterObservation(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	event := replayEvent(now, "event-1", target("save"))
	version := replayVersion(event)
	fake := adapter.NewFakeAdapter(action.CapabilityTap)
	fake.QueueObservation(adapter.Observation{Token: "before-runtime", PackageName: "com.example", ActivityName: ".Main", AppVersion: "1.0.0", CoordinateSpace: "display:1080x1920", DisplayWidth: 1080, DisplayHeight: 1920, Nodes: []adapter.TargetNode{{ResourceID: "save", Actionable: true, Enabled: true}}})
	fake.QueueObservation(adapter.Observation{Token: "after-runtime", PackageName: "com.example", ActivityName: ".Main", AppVersion: "1.0.0", CoordinateSpace: "display:1080x1920", DisplayWidth: 1080, DisplayHeight: 1920})
	fake.QueueExecution(adapter.ExecutionScript{Execution: adapter.Execution{Outcome: action.OutcomeVerified, Postcondition: action.PostconditionPassed, Dispatched: true}})
	results, err := recordings.Replay(context.Background(), version, []recordings.InteractionEvent{event}, fake, replayAuthorization(), nil)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if len(results) != 1 || results[0].Outcome != action.OutcomeVerified || results[0].Resolution != recordings.ResolutionResourceID || results[0].AfterToken != "after-runtime" || results[0].Failure != "" {
		t.Fatalf("replay results = %#v, want verified semantic replay", results)
	}
	if fake.ExecuteCalls() != 1 || fake.CleanupCalls() != 1 {
		t.Fatalf("fake calls = execute:%d cleanup:%d, want one of each", fake.ExecuteCalls(), fake.CleanupCalls())
	}
}

func TestReplayFailsClosedForAmbiguousTargetAndWrongAppIdentity(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	event := replayEvent(now, "event-1", target("save"))
	version := replayVersion(event)
	fake := adapter.NewFakeAdapter(action.CapabilityTap)
	fake.QueueObservation(adapter.Observation{Token: "ambiguous", PackageName: "com.example", ActivityName: ".Main", AppVersion: "1.0.0", CoordinateSpace: "display:1080x1920", Nodes: []adapter.TargetNode{{ResourceID: "save", Actionable: true, Enabled: true}, {ResourceID: "save", Actionable: true, Enabled: true}}})
	results, err := recordings.Replay(context.Background(), version, []recordings.InteractionEvent{event}, fake, replayAuthorization(), nil)
	if err != nil || len(results) != 1 || results[0].Failure != domain.FailureAmbiguousTarget || fake.ExecuteCalls() != 0 {
		t.Fatalf("ambiguous replay = results:%#v error:%v execute:%d", results, err, fake.ExecuteCalls())
	}

	wrongApp := adapter.NewFakeAdapter(action.CapabilityTap)
	wrongApp.QueueObservation(adapter.Observation{Token: "wrong-app", PackageName: "com.other", ActivityName: ".Main", AppVersion: "1.0.0", CoordinateSpace: "display:1080x1920"})
	results, err = recordings.Replay(context.Background(), version, []recordings.InteractionEvent{event}, wrongApp, replayAuthorization(), nil)
	if err != nil || len(results) != 1 || results[0].Failure != domain.FailureUnknownScreen || wrongApp.ExecuteCalls() != 0 {
		t.Fatalf("wrong-app replay = results:%#v error:%v execute:%d", results, err, wrongApp.ExecuteCalls())
	}
}

func TestReplayUsesOnlyExplicitBoundedCoordinateFallbackAndPreservesIndeterminate(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	event := replayEvent(now, "event-1", target("save"))
	coordinate := action.Coordinate{Space: "display:1080x1920", X: 100, Y: 200}
	event.Action.Coordinate = &coordinate
	version := replayVersion(event)
	fake := adapter.NewFakeAdapter(action.CapabilityTap)
	fake.QueueObservation(adapter.Observation{Token: "before-runtime", PackageName: "com.example", ActivityName: ".Main", AppVersion: "1.0.0", CoordinateSpace: coordinate.Space, DisplayWidth: 1080, DisplayHeight: 1920})
	fake.QueueExecution(adapter.ExecutionScript{Execution: adapter.Execution{Dispatched: true}, Err: &adapter.ExecutionError{Cause: errors.New("transport lost"), Dispatched: true, FailureClass: domain.FailureTransport}})
	authorization := replayAuthorization()
	authorization.Policy.AllowCoordinateFallback = true
	authorization.Policy.OperatorConfirmed = true
	results, err := recordings.Replay(context.Background(), version, []recordings.InteractionEvent{event}, fake, authorization, nil)
	if err != nil || len(results) != 1 || results[0].Resolution != recordings.ResolutionCoordinate || results[0].Outcome != action.OutcomeIndeterminate || results[0].Failure != domain.FailureIndeterminate {
		t.Fatalf("coordinate indeterminate replay = results:%#v error:%v", results, err)
	}

	noConfirmation := adapter.NewFakeAdapter(action.CapabilityTap)
	noConfirmation.QueueObservation(adapter.Observation{Token: "before-runtime", PackageName: "com.example", ActivityName: ".Main", AppVersion: "1.0.0", CoordinateSpace: coordinate.Space, DisplayWidth: 1080, DisplayHeight: 1920})
	noConfirmationAuthorization := replayAuthorization()
	noConfirmationAuthorization.Policy.AllowCoordinateFallback = true
	results, err = recordings.Replay(context.Background(), version, []recordings.InteractionEvent{event}, noConfirmation, noConfirmationAuthorization, nil)
	if err != nil || len(results) != 1 || results[0].Failure != domain.FailureUnknownScreen || noConfirmation.ExecuteCalls() != 0 {
		t.Fatalf("unconfirmed coordinate replay = results:%#v error:%v", results, err)
	}
}

func TestReplayFallsBackToContextFingerprintAfterStableText(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	event := replayEvent(now, "event-1", recordings.TargetMetadata{Semantic: action.SemanticTarget{ContextFingerprint: "screen:save"}, Actionable: true, Enabled: true, CandidateCount: 1, Confidence: 1})
	version := replayVersion(event)
	fake := adapter.NewFakeAdapter(action.CapabilityTap)
	fake.QueueObservation(adapter.Observation{Token: "before-runtime", PackageName: "com.example", ActivityName: ".Main", AppVersion: "1.0.0", CoordinateSpace: "display:1080x1920", DisplayWidth: 1080, DisplayHeight: 1920, Nodes: []adapter.TargetNode{{ContextFingerprint: "screen:save", Actionable: true, Enabled: true}}})
	fake.QueueObservation(adapter.Observation{Token: "after-runtime", PackageName: "com.example", ActivityName: ".Main", AppVersion: "1.0.0", CoordinateSpace: "display:1080x1920", DisplayWidth: 1080, DisplayHeight: 1920})
	fake.QueueExecution(adapter.ExecutionScript{Execution: adapter.Execution{Outcome: action.OutcomeVerified, Postcondition: action.PostconditionPassed, Dispatched: true}})
	results, err := recordings.Replay(context.Background(), version, []recordings.InteractionEvent{event}, fake, replayAuthorization(), nil)
	if err != nil || len(results) != 1 || results[0].Resolution != recordings.ResolutionContext || results[0].Outcome != action.OutcomeVerified {
		t.Fatalf("context replay = results:%#v error:%v", results, err)
	}
}

func TestReplayCarriesTypedTextPayloadToTheAdapter(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	event := replayEvent(now, "event-1", target("field"))
	event.Action.Kind = action.TextInput
	event.Action.DisplayValue = "safe-value"
	event.Action.ValueLength = len(event.Action.DisplayValue)
	version := replayVersion(event)
	version.Steps[0].Action = action.TextInput
	version.Steps[0].Risk = action.RiskHigh
	version.Steps[0].Retry = action.RetryNeverBlind
	version.Steps[0].Definition.TextValue = event.Action.DisplayValue
	version.Steps[0].Definition.ValueLength = event.Action.ValueLength
	fake := adapter.NewFakeAdapter(action.CapabilityTextInput)
	fake.QueueObservation(adapter.Observation{Token: "before-runtime", PackageName: "com.example", ActivityName: ".Main", AppVersion: "1.0.0", CoordinateSpace: "display:1080x1920", DisplayWidth: 1080, DisplayHeight: 1920, Nodes: []adapter.TargetNode{{ResourceID: "field", Actionable: true, Enabled: true}}})
	fake.QueueObservation(adapter.Observation{Token: "after-runtime", PackageName: "com.example", ActivityName: ".Main", AppVersion: "1.0.0", CoordinateSpace: "display:1080x1920", DisplayWidth: 1080, DisplayHeight: 1920})
	fake.QueueExecution(adapter.ExecutionScript{Execution: adapter.Execution{Outcome: action.OutcomeVerified, Postcondition: action.PostconditionPassed, Dispatched: true}})
	results, err := recordings.Replay(context.Background(), version, []recordings.InteractionEvent{event}, fake, replayAuthorizationWithCapability(action.CapabilityTextInput), nil)
	if err != nil || len(results) != 1 || results[0].Outcome != action.OutcomeVerified {
		t.Fatalf("typed text replay = results:%#v error:%v", results, err)
	}
	intents := fake.Intents()
	if len(intents) != 1 || intents[0].TextValue != "safe-value" || intents[0].ValueLength != len("safe-value") {
		t.Fatalf("executed intents = %#v, want typed text payload", intents)
	}
}

func TestReplayChecksTypedPolicyBeforeObservationOrDispatch(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	event := replayEvent(now, "event-1", target("save"))
	version := replayVersion(event)
	fake := adapter.NewFakeAdapter(action.CapabilityTap)
	authorization := replayAuthorization()
	authorization.PolicyRuleJSON = `{"allow":false}`
	results, err := recordings.Replay(context.Background(), version, []recordings.InteractionEvent{event}, fake, authorization, nil)
	if err != nil || len(results) != 1 || results[0].Failure != domain.FailurePolicyDenied || fake.ExecuteCalls() != 0 {
		t.Fatalf("policy-denied replay = results:%#v error:%v execute:%d", results, err, fake.ExecuteCalls())
	}
}

func replayVersion(event recordings.InteractionEvent) workflows.Version {
	return workflows.Version{ID: "skill-version-1", Workspace: "workspace-1", WorkflowID: "skill-1", Version: 1, State: workflows.StatePublished, Steps: []workflows.Step{{ID: workflows.StepID(event.ID), Sequence: 0, Action: action.Tap, Risk: action.RiskMedium, Retry: action.RetryAfterObservation, Definition: workflows.StepDefinition{Target: event.Action.Target.Semantic, TimeoutMillis: 1000, RequiresObservation: true, EvidenceRequired: true, Postcondition: "after-captured"}}}}
}

func replayEvent(now time.Time, id string, target recordings.TargetMetadata) recordings.InteractionEvent {
	started := now
	reviewed := now.Add(2 * time.Second)
	return recordings.InteractionEvent{ID: recordings.RecordingEventID(id), Workspace: "workspace-1", SessionID: "session-1", Sequence: 0, CorrelationID: "corr-1", Action: recordings.LogicalAction{Kind: action.Tap, Target: target, Sensitivity: recordings.SensitivityNone, TimeoutMillis: 1000, Postcondition: "after-captured"}, Before: replayCapture(started, "before"), After: replayCapture(started.Add(time.Second), "after"), Redaction: recordings.RedactionNotRequired, Review: recordings.ReviewApproved, ReviewerID: "operator-1", ReviewedAt: &reviewed, StartedAt: started, FinishedAt: started.Add(100 * time.Millisecond), CreatedAt: started}
}

func replayCapture(at time.Time, id string) *recordings.Capture {
	return &recordings.Capture{ObservationID: id, FreshnessToken: id + "-fresh", CapturedAt: at, CoordinateSpace: "display:1080x1920", PackageName: "com.example", ActivityName: ".Main", AppVersion: "1.0.0", DisplayWidth: 1080, DisplayHeight: 1920, Status: recordings.CaptureComplete, Sanitization: recordings.Sanitized}
}

func replayAuthorization() recordings.ReplayAuthorization {
	return replayAuthorizationWithCapability(action.CapabilityTap)
}

func replayAuthorizationWithCapability(capability action.Capability) recordings.ReplayAuthorization {
	return recordings.ReplayAuthorization{Workspace: "workspace-1", DeviceID: "device-1", LeaseID: "lease-1", HolderID: "holder-1", FencingToken: 1, ApprovalGranted: true, Capabilities: []action.Capability{capability}, PolicyRuleJSON: `{}`, Policy: recordings.ReplayPolicy{ExpectedPackageName: "com.example", ExpectedActivityName: ".Main", ExpectedAppVersion: "1.0.0", ExpectedCoordinateSpace: "display:1080x1920"}}
}
