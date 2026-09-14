package skills_test

import (
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/recordings"
	"drift.local/drift-next/internal/skills"
)

func TestCompileReviewAndPublishRecordingAsImmutableTypedSkill(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	finished := now.Add(time.Minute)
	session := recordings.Session{ID: "session-1", Workspace: "workspace-1", Number: 1, State: recordings.SessionCompleted, Source: recordings.SourceFake, StartedAt: &[]time.Time{now}[0], FinishedAt: &finished, CreatedAt: now, Review: recordings.ReviewUnreviewed, Cleanup: recordings.CleanupNone}
	event := approvedEvent(now, session.ID, 0, action.Tap, target("save"))
	version, err := skills.CompileRecording(skills.CompileRequest{ID: "skill-version-1", Workspace: "workspace-1", SkillID: "skill-1", Version: 1, SourceRecordingSession: session.ID, Now: now.Add(2 * time.Minute)}, session, []recordings.InteractionEvent{event})
	if err != nil {
		t.Fatalf("CompileRecording() error = %v", err)
	}
	if version.State != skills.Draft || version.Trust != skills.TrustUnreviewed || len(version.Steps) != 1 || version.Steps[0].Definition.Target.ResourceID != "save" {
		t.Fatalf("compiled version = %#v, want immutable typed draft", version)
	}
	reviewed, review, err := skills.Review(version, "operator-1", "reviewed sanitized fake demonstration", now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if reviewed.State != skills.Validated || reviewed.Trust != skills.TrustApproved || review.To != skills.Validated {
		t.Fatalf("reviewed version = %#v promotion = %#v", reviewed, review)
	}
	published, publish, err := skills.Publish(reviewed, "operator-1", "publish reviewed skill", now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if published.State != skills.Published || publish.To != skills.Published {
		t.Fatalf("published version = %#v promotion = %#v", published, publish)
	}
	definition, err := published.DefinitionJSON()
	if err != nil || strings.Contains(strings.ToLower(definition), "shell") || !strings.Contains(definition, "save") {
		t.Fatalf("DefinitionJSON() = %q, error = %v", definition, err)
	}
}

func TestCompileRejectsUnreviewedAmbiguousSensitiveAndCoordinateOnlyEvents(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	finished := now.Add(time.Minute)
	session := recordings.Session{ID: "session-1", Workspace: "workspace-1", Number: 1, State: recordings.SessionCompleted, Source: recordings.SourceFake, StartedAt: &[]time.Time{now}[0], FinishedAt: &finished, CreatedAt: now, Cleanup: recordings.CleanupNone, Review: recordings.ReviewUnreviewed}
	cases := []struct {
		name  string
		event recordings.InteractionEvent
	}{
		{name: "unreviewed", event: approvedEvent(now, session.ID, 0, action.Tap, target("save"))},
		{name: "ambiguous", event: approvedEvent(now, session.ID, 0, action.Tap, recordings.TargetMetadata{Semantic: action.SemanticTarget{StableText: "Save"}, Actionable: true, Enabled: true, CandidateCount: 2, Confidence: 0.5})},
		{name: "sensitive", event: sensitiveEvent(now, session.ID)},
		{name: "coordinate-only", event: approvedEvent(now, session.ID, 0, action.Tap, recordings.TargetMetadata{Bounds: [4]int{1, 1, 20, 20}, Actionable: true, Enabled: true, CandidateCount: 0, Confidence: 1})},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.name == "unreviewed" {
				testCase.event.Review = recordings.ReviewUnreviewed
				testCase.event.ReviewerID = ""
				testCase.event.ReviewedAt = nil
			} else {
				testCase.event.Review = recordings.ReviewApproved
				testCase.event.ReviewerID = "operator-1"
				reviewedAt := now.Add(time.Second)
				testCase.event.ReviewedAt = &reviewedAt
			}
			if _, err := skills.CompileRecording(skills.CompileRequest{ID: "skill-version-1", Workspace: "workspace-1", SkillID: "skill-1", Version: 1, Now: now.Add(2 * time.Minute)}, session, []recordings.InteractionEvent{testCase.event}); err == nil {
				t.Fatal("CompileRecording() error = nil, want promotion rejection")
			}
		})
	}
}

func TestSharedBrainKnowledgeIsValidatedAndSeparateFromExecution(t *testing.T) {
	valid := skills.BrainKnowledge{ID: "knowledge-1", Workspace: "workspace-1", Key: "login-screen", Version: 1, State: skills.Validated, KnowledgeJSON: `{"package":"com.example","screen":"login"}`}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid knowledge rejected: %v", err)
	}
	unsafe := valid
	unsafe.KnowledgeJSON = `{"token":"secret-value"}`
	if err := unsafe.Validate(); err == nil {
		t.Fatal("unsafe knowledge error = nil")
	}
}

func approvedEvent(now time.Time, sessionID recordings.RecordingSessionID, sequence int, kind action.Kind, target recordings.TargetMetadata) recordings.InteractionEvent {
	return recordings.InteractionEvent{ID: recordings.RecordingEventID("event-" + string(rune('1'+sequence))), Workspace: "workspace-1", SessionID: sessionID, Sequence: sequence, CorrelationID: "corr-" + string(rune('1'+sequence)), Action: recordings.LogicalAction{Kind: kind, Target: target, Sensitivity: recordings.SensitivityNone, TimeoutMillis: 1000, Postcondition: "after-captured"}, Before: capture(now, "before"), After: capture(now.Add(time.Second), "after"), Redaction: recordings.RedactionNotRequired, Review: recordings.ReviewApproved, ReviewerID: "operator-1", ReviewedAt: &[]time.Time{now.Add(time.Second)}[0], StartedAt: now, FinishedAt: now.Add(time.Second), CreatedAt: now}
}

func sensitiveEvent(now time.Time, sessionID recordings.RecordingSessionID) recordings.InteractionEvent {
	event := approvedEvent(now, sessionID, 0, action.TextInput, target("password"))
	event.Action.DisplayValue = "[REDACTED]"
	event.Action.Sensitivity = recordings.SensitivitySensitive
	event.Redaction = recordings.RedactionRedacted
	event.Sensitive = true
	event.Action.ValueLength = 4
	return event
}

func capture(at time.Time, id string) *recordings.Capture {
	return &recordings.Capture{ObservationID: id, FreshnessToken: id + "-fresh", CapturedAt: at, CoordinateSpace: "display:1080x1920", PackageName: "com.example", ActivityName: ".Main", AppVersion: "1.0.0", Status: recordings.CaptureComplete, Sanitization: recordings.Sanitized, Evidence: []recordings.EvidenceReference{{Phase: recordings.PhaseBefore, Kind: recordings.EvidenceRawScreenshot, ContentHash: "sha256:" + id, MediaType: "image/png", SchemaVersion: 1}}}
}

func target(resourceID string) recordings.TargetMetadata {
	return recordings.TargetMetadata{Semantic: action.SemanticTarget{ResourceID: resourceID}, Actionable: true, Enabled: true, CandidateCount: 1, Confidence: 1, Source: "fake"}
}
