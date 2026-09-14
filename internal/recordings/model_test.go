package recordings_test

import (
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/recordings"
)

func TestRecordingEventRejectsPlaintextSensitiveInput(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	event := recordings.InteractionEvent{
		ID:            "event-1",
		Workspace:     "workspace-1",
		SessionID:     "session-1",
		Sequence:      0,
		CorrelationID: "corr-1",
		Action: recordings.LogicalAction{
			Kind:          action.TextInput,
			Target:        target("password-field"),
			DisplayValue:  "hunter2",
			ValueLength:   7,
			Sensitivity:   recordings.SensitivitySensitive,
			TimeoutMillis: 1000,
			Postcondition: "field-updated",
		},
		Redaction:  recordings.RedactionRedacted,
		Sensitive:  true,
		Review:     recordings.ReviewUnreviewed,
		StartedAt:  now,
		FinishedAt: now.Add(time.Second),
		CreatedAt:  now,
	}
	if err := event.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want plaintext-sensitive rejection")
	}
}

func TestRecordingEventAcceptsSafeRedactedSensitiveMetadata(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	event := recordings.InteractionEvent{
		ID:            "event-1",
		Workspace:     "workspace-1",
		SessionID:     "session-1",
		Sequence:      0,
		CorrelationID: "corr-1",
		Action: recordings.LogicalAction{
			Kind:          action.TextInput,
			Target:        target("password-field"),
			DisplayValue:  "[REDACTED]",
			ValueLength:   7,
			Sensitivity:   recordings.SensitivitySensitive,
			TimeoutMillis: 1000,
			Postcondition: "field-updated",
		},
		Redaction:  recordings.RedactionRedacted,
		Sensitive:  true,
		Review:     recordings.ReviewUnreviewed,
		StartedAt:  now,
		FinishedAt: now.Add(time.Second),
		CreatedAt:  now,
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDeleteCompletedRequiresConfirmationAndLeavesCleanupPending(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	started := now
	finished := now.Add(time.Minute)
	session := recordings.Session{ID: "session-1", Workspace: "workspace-1", Number: 1, State: recordings.SessionCompleted, Source: recordings.SourceFake, StartedAt: &started, FinishedAt: &finished, CreatedAt: now}
	if _, err := recordings.DeleteCompleted(session, false, now.Add(2*time.Minute)); err == nil {
		t.Fatal("DeleteCompleted() error = nil, want confirmation error")
	}
	deleted, err := recordings.DeleteCompleted(session, true, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("DeleteCompleted() error = %v", err)
	}
	if deleted.DeletedAt == nil || deleted.Cleanup != recordings.CleanupPending {
		t.Fatalf("deleted session = %#v, want tombstone and pending cleanup", deleted)
	}
}

func target(resourceID string) recordings.TargetMetadata {
	return recordings.TargetMetadata{Semantic: action.SemanticTarget{ResourceID: resourceID}, Actionable: true, Enabled: true, Source: "fake", Confidence: 1, CandidateCount: 1}
}
