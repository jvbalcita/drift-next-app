package sqlite_test

import (
	"context"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/recordings"
	"drift.local/drift-next/internal/skills"
	store "drift.local/drift-next/internal/store/sqlite"
	"drift.local/drift-next/internal/workflows"
)

func TestRecordingServicePersistsReviewableEvidenceAndTombstone(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	workspace := organizations.Workspace{ID: "recordings-w", Name: "Recordings", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(ctx, workspace, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	recordingService := store.NewRecordingService(db)
	session, err := recordingService.Create(ctx, recordings.Session{ID: "recording-1", Workspace: workspace.ID, Source: recordings.SourceFake, CreatedAt: now}, "operator", "op-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if session.Number != 1 || session.State != recordings.SessionRequested {
		t.Fatalf("created session = %#v, want first requested session", session)
	}
	if _, err := recordingService.Start(ctx, workspace.ID, session.ID, "operator", "op-1"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	eventTime := time.Now().UTC()
	event := persistedTestEvent(eventTime, workspace.ID, session.ID)
	if err := recordingService.AppendEvent(ctx, event, "recorder", "fake-recorder"); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	sensitive := persistedTestEvent(eventTime.Add(2*time.Second), workspace.ID, session.ID)
	sensitive.ID = "event-sensitive"
	sensitive.Sequence = 1
	sensitive.Action.Kind = action.TextInput
	sensitive.Action.Target = recordings.TargetMetadata{Semantic: action.SemanticTarget{ResourceID: "password-field"}, Actionable: true, Enabled: true, CandidateCount: 1, Confidence: 1, Source: "fake"}
	sensitive.Action.DisplayValue = "[REDACTED]"
	sensitive.Action.ValueLength = 8
	sensitive.Action.Sensitivity = recordings.SensitivitySensitive
	sensitive.Redaction = recordings.RedactionRedacted
	sensitive.Sensitive = true
	if err := recordingService.AppendEvent(ctx, sensitive, "recorder", "fake-recorder"); err != nil {
		t.Fatalf("AppendEvent(sensitive) error = %v", err)
	}
	storedSensitive, err := store.NewRecordingRepository(db).GetEvent(ctx, workspace.ID, sensitive.ID)
	if err != nil {
		t.Fatalf("GetEvent(sensitive) error = %v", err)
	}
	if storedSensitive.Action.Target.Semantic.ResourceID != "" || storedSensitive.Action.DisplayValue != "[REDACTED]" {
		t.Fatalf("stored sensitive action = %#v, want stripped target and redacted value", storedSensitive.Action)
	}
	reviewed, err := recordingService.ReviewEvent(ctx, workspace.ID, event.ID, true, "reviewer-1")
	if err != nil {
		t.Fatalf("ReviewEvent() error = %v", err)
	}
	if reviewed.Review != recordings.ReviewApproved || reviewed.ReviewerID != "reviewer-1" {
		t.Fatalf("reviewed event = %#v", reviewed)
	}
	completed, err := recordingService.Stop(ctx, workspace.ID, session.ID, "operator", "op-1")
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if completed.State != recordings.SessionCompleted {
		t.Fatalf("completed session state = %q", completed.State)
	}

	repository := store.NewRecordingRepository(db)
	storedSession, err := repository.GetSession(ctx, workspace.ID, session.ID)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if storedSession.State != recordings.SessionCompleted || storedSession.Number != 1 {
		t.Fatalf("stored session = %#v", storedSession)
	}
	storedEvent, err := repository.GetEvent(ctx, workspace.ID, event.ID)
	if err != nil {
		t.Fatalf("GetEvent() error = %v", err)
	}
	if storedEvent.Review != recordings.ReviewApproved || storedEvent.Action.Target.Semantic.ResourceID != "save" {
		t.Fatalf("stored event = %#v", storedEvent)
	}
	phases := make(map[recordings.CapturePhase]bool)
	for _, reference := range storedEvent.Evidence {
		phases[reference.Phase] = true
	}
	if !phases[recordings.PhaseBefore] || !phases[recordings.PhaseAction] || !phases[recordings.PhaseAfter] {
		t.Fatalf("stored evidence phases = %#v, want before/action/after", phases)
	}

	if _, err := recordingService.DeleteSession(ctx, workspace.ID, session.ID, false, "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodeConflict {
		t.Fatalf("unconfirmed deletion code = %v, want conflict", platformerrors.CodeOf(err))
	}
	deleted, err := recordingService.DeleteSession(ctx, workspace.ID, session.ID, true, "operator", "op-1")
	if err != nil {
		t.Fatalf("DeleteSession() error = %v", err)
	}
	if deleted.DeletedAt == nil || deleted.Cleanup != recordings.CleanupPending {
		t.Fatalf("deleted session = %#v, want tombstone and pending cleanup", deleted)
	}
	cleaned, err := recordingService.MarkCleanup(ctx, workspace.ID, session.ID, recordings.CleanupDone, "operator", "op-1")
	if err != nil {
		t.Fatalf("MarkCleanup() error = %v", err)
	}
	if cleaned.Cleanup != recordings.CleanupDone {
		t.Fatalf("cleanup state = %q, want done", cleaned.Cleanup)
	}
}

func TestSkillServicePersistsTypedVersionAndExplicitPromotionHistory(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	workspace := organizations.Workspace{ID: "skills-w", Name: "Skills", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(ctx, workspace, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}

	skillService := store.NewSkillService(db)
	skill, err := skillService.Create(ctx, skills.Skill{ID: "skill-1", Workspace: workspace.ID, Name: "Save item"}, "operator", "op-1")
	if err != nil {
		t.Fatalf("Create skill error = %v", err)
	}
	recordingService := store.NewRecordingService(db)
	session, err := recordingService.Create(ctx, recordings.Session{ID: "source-recording", Workspace: workspace.ID, Source: recordings.SourceFake, CreatedAt: time.Now().UTC()}, "operator", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recordingService.Start(ctx, workspace.ID, session.ID, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := recordingService.Stop(ctx, workspace.ID, session.ID, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}

	version, err := skillService.CreateVersion(ctx, workspace.ID, skills.SkillVersion{
		ID:                     "skill-version-1",
		SkillID:                skill.ID,
		Version:                1,
		Manifest:               skills.Manifest{RequestedCapabilities: []action.Capability{action.CapabilityTap}, Fixtures: []string{"sha256:fake"}, Risk: action.RiskMedium, Retry: action.RetryAfterObservation},
		SourceRecordingSession: session.ID,
		Steps:                  []workflows.Step{{ID: "step-1", Sequence: 0, Action: action.Tap, Risk: action.RiskMedium, Retry: action.RetryAfterObservation, Definition: workflows.StepDefinition{Target: action.SemanticTarget{ResourceID: "save"}, TimeoutMillis: 1000, RequiresObservation: true, EvidenceRequired: true, Postcondition: "screen_changed"}}},
	}, "operator", "op-1")
	if err != nil {
		t.Fatalf("CreateVersion() error = %v", err)
	}
	if version.State != skills.Draft || version.Trust != skills.TrustUnreviewed {
		t.Fatalf("created version = %#v, want unreviewed draft", version)
	}

	repository := store.NewSkillRepository(db)
	stored, err := repository.GetVersion(ctx, workspace.ID, version.ID)
	if err != nil {
		t.Fatalf("GetVersion() error = %v", err)
	}
	if stored.SourceRecordingSession != session.ID || len(stored.Steps) != 1 || stored.Steps[0].Definition.Target.ResourceID != "save" {
		t.Fatalf("stored version = %#v", stored)
	}
	reviewed, review, err := skillService.ReviewVersion(ctx, workspace.ID, version.ID, "reviewer-1", "reviewed fake recording")
	if err != nil {
		t.Fatalf("ReviewVersion() error = %v", err)
	}
	if review.ID == "" || reviewed.State != skills.Validated || reviewed.Trust != skills.TrustApproved {
		t.Fatalf("review result = %#v promotion = %#v", reviewed, review)
	}
	published, publish, err := skillService.PublishVersion(ctx, workspace.ID, version.ID, "publisher-1", "publish reviewed skill")
	if err != nil {
		t.Fatalf("PublishVersion() error = %v", err)
	}
	if publish.ID == "" || published.State != skills.Published {
		t.Fatalf("publish result = %#v promotion = %#v", published, publish)
	}
	storedSkill, err := repository.Get(ctx, workspace.ID, skill.ID)
	if err != nil {
		t.Fatalf("Get skill error = %v", err)
	}
	if storedSkill.State != skills.Published {
		t.Fatalf("stored skill state = %q, want published", storedSkill.State)
	}
	versions, err := repository.ListVersions(ctx, workspace.ID, skill.ID)
	if err != nil || len(versions) != 1 || versions[0].State != skills.Published {
		t.Fatalf("ListVersions() = %#v, error = %v", versions, err)
	}

	knowledge, err := skillService.RecordBrainKnowledge(ctx, skills.BrainKnowledge{ID: "brain-1", Workspace: workspace.ID, Key: "save-screen", Version: 1, State: skills.Validated, KnowledgeJSON: `{"package":"com.example","screen":"save"}`, SourceSkillVersion: published.ID}, "operator", "op-1")
	if err != nil {
		t.Fatalf("RecordBrainKnowledge() error = %v", err)
	}
	storedKnowledge, err := repository.GetBrainKnowledge(ctx, workspace.ID, knowledge.ID)
	if err != nil {
		t.Fatalf("GetBrainKnowledge() error = %v", err)
	}
	if storedKnowledge.SourceSkillVersion != published.ID || storedKnowledge.KnowledgeJSON != knowledge.KnowledgeJSON {
		t.Fatalf("stored shared-brain knowledge = %#v", storedKnowledge)
	}
}

func persistedTestEvent(at time.Time, workspace organizations.WorkspaceID, session recordings.RecordingSessionID) recordings.InteractionEvent {
	return recordings.InteractionEvent{
		ID: "event-1", Workspace: workspace, SessionID: session, Sequence: 0, CorrelationID: "corr-1",
		Action:    recordings.LogicalAction{Kind: action.Tap, Target: recordings.TargetMetadata{Semantic: action.SemanticTarget{ResourceID: "save"}, Actionable: true, Enabled: true, CandidateCount: 1, Confidence: 1, Source: "fake"}, Sensitivity: recordings.SensitivityNone, TimeoutMillis: 1000, Postcondition: "screen_changed"},
		Before:    &recordings.Capture{CapturedAt: at, CoordinateSpace: "display:1080x1920", PackageName: "com.example", ActivityName: ".Main", AppVersion: "1.0.0", ScreenshotHash: "before-hash", Status: recordings.CaptureComplete, Sanitization: recordings.Sanitized, Evidence: []recordings.EvidenceReference{{Phase: recordings.PhaseBefore, Kind: recordings.EvidenceRawScreenshot, ContentHash: "before-hash", MediaType: "image/png", SchemaVersion: 1, Authoritative: true}}},
		After:     &recordings.Capture{CapturedAt: at.Add(time.Second), CoordinateSpace: "display:1080x1920", PackageName: "com.example", ActivityName: ".Main", AppVersion: "1.0.0", ScreenshotHash: "after-hash", Status: recordings.CaptureComplete, Sanitization: recordings.Sanitized, Evidence: []recordings.EvidenceReference{{Phase: recordings.PhaseAfter, Kind: recordings.EvidenceUITree, ContentHash: "after-tree", MediaType: "application/xml", SchemaVersion: 1, Authoritative: true}}},
		Evidence:  []recordings.EvidenceReference{{Phase: recordings.PhaseAction, Kind: recordings.EvidenceAnnotatedScreenshot, ContentHash: "annotated-hash", MediaType: "image/png", SchemaVersion: 1}},
		Redaction: recordings.RedactionNotRequired, Review: recordings.ReviewUnreviewed, StartedAt: at, FinishedAt: at.Add(time.Second), CreatedAt: at,
	}
}
