package sqlite_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

// This file is the store-level half of the action evidence boundary (ARC-64):
// the append-only history that a later recording can reference instead of
// re-stating what was intended. Every record here is disposable test data: no
// device, no adb process, no credential, no typed content.

func actionEvidenceStoreFixture(t *testing.T) (*store.DB, organizations.WorkspaceID) {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.Workspace{ID: "evidence-w", Name: "Evidence", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(ctx, workspace, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{ID: "device-1", Workspace: workspace.ID, DisplayName: "Fake", PlatformVersion: "fake", State: devices.Registered}, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	return db, workspace.ID
}

func actionEvidenceFixture(workspace organizations.WorkspaceID, attemptID string) store.ActionEvidence {
	return store.ActionEvidence{
		Workspace:         string(workspace),
		DeviceID:          "device-1",
		Serial:            "emulator-1",
		AttemptID:         attemptID,
		Kind:              action.Tap,
		InvocationSurface: action.SurfaceManual,
		Disposition:       store.EvidenceDispatched,
		Outcome:           action.OutcomeVerified,
		Postcondition:     action.PostconditionPassed,
		Observation:       store.ActionObservation{Token: "obs-fresh-1", ForegroundPackage: "com.example.fake", FieldLength: 12},
	}
}

// TestActionEvidenceIsAppendOnlyAndNeverRewrittenByALaterAttempt asserts the two
// properties the card turns on. A second dispatch of the same action appends its
// own record, the first record is byte-identical afterwards, and the append-only
// property is the schema's rather than the repository's SQL alone: the database
// itself refuses to rewrite or erase a record.
func TestActionEvidenceIsAppendOnlyAndNeverRewrittenByALaterAttempt(t *testing.T) {
	db, workspace := actionEvidenceStoreFixture(t)
	ctx := context.Background()
	service := store.NewActionEvidenceService(db)
	repository := store.NewActionEvidenceRepository(db)

	first := actionEvidenceFixture(workspace, "attempt-1")
	if err := service.Append(ctx, first, "operator", "operator-1"); err != nil {
		t.Fatalf("append first evidence: %v", err)
	}
	firstRead, err := repository.ListForAttempt(ctx, workspace, first.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstRead) != 1 {
		t.Fatalf("evidence for one attempt = %d records, want 1", len(firstRead))
	}
	if firstRead[0].Disposition != store.EvidenceDispatched || firstRead[0].Observation.Token != "obs-fresh-1" || firstRead[0].Observation.FieldLength != 12 {
		t.Fatalf("first record = %#v, want the dispatched observation", firstRead[0])
	}
	if firstRead[0].RecordedAt.IsZero() || firstRead[0].SchemaVersion != store.ActionEvidenceSchemaVersion {
		t.Fatalf("first record identity = %#v, want a recorded time and the current schema version", firstRead[0])
	}

	// The same action, dispatched a second time, is a second record.
	second := actionEvidenceFixture(workspace, "attempt-1")
	second.Disposition = store.EvidenceRefused
	second.Outcome = ""
	second.Postcondition = ""
	second.Observation = store.ActionObservation{}
	second.FailureClass = domain.FailureLeaseConflict
	second.RefusalReason = "lease_expired"
	if err := service.Append(ctx, second, "operator", "operator-1"); err != nil {
		t.Fatalf("append second evidence: %v", err)
	}
	afterSecond, err := repository.ListForAttempt(ctx, workspace, first.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterSecond) != 2 {
		t.Fatalf("evidence after a second dispatch = %d records, want 2", len(afterSecond))
	}
	if !reflect.DeepEqual(firstRead[0], afterSecond[0]) {
		t.Fatalf("a later attempt rewrote the first record:\n before %#v\n after  %#v", firstRead[0], afterSecond[0])
	}
	if afterSecond[1].Disposition != store.EvidenceRefused || afterSecond[1].Observation.Token != "" {
		t.Fatalf("second record = %#v, want a refusal with no observation", afterSecond[1])
	}

	// Append-only is enforced by the database, so it survives a caller that
	// reaches the table without going through the repository.
	raw := store.SQLForTest(db)
	if _, err := raw.ExecContext(ctx, `UPDATE action_evidence SET outcome = 'failed' WHERE attempt_id = 'attempt-1'`); err == nil {
		t.Fatal("the database allowed an evidence record to be rewritten")
	}
	if _, err := raw.ExecContext(ctx, `DELETE FROM action_evidence WHERE attempt_id = 'attempt-1'`); err == nil {
		t.Fatal("the database allowed an evidence record to be erased")
	}
	final, err := repository.ListForAttempt(ctx, workspace, first.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if len(final) != 2 || !reflect.DeepEqual(final[0], firstRead[0]) {
		t.Fatalf("evidence history after a refused rewrite = %#v, want the original two records", final)
	}

	// And the history is readable per device, which is how a recording resolves
	// what happened to a target rather than what was intended for it. The read
	// is in append order, which is not the recorded time: two appends can share
	// one timestamp, and this history must still be readable in order.
	byDevice, err := repository.ListForDevice(ctx, workspace, "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(byDevice) != 2 {
		t.Fatalf("evidence for device-1 = %d records, want both", len(byDevice))
	}
	if byDevice[0].Disposition != store.EvidenceDispatched || byDevice[1].Disposition != store.EvidenceRefused {
		t.Fatalf("device evidence order = (%q, %q), want the append order", byDevice[0].Disposition, byDevice[1].Disposition)
	}
}

// TestActionEvidenceRefusesARecordThatCouldCarryContentOrACredential asserts the
// store is a second gate: even a caller that skipped the dispatch boundary's
// redaction cannot persist a record whose fields could hold typed content, a
// handle to it, a credential-shaped value or an unbounded free-form string.
func TestActionEvidenceRefusesARecordThatCouldCarryContentOrACredential(t *testing.T) {
	db, workspace := actionEvidenceStoreFixture(t)
	ctx := context.Background()
	service := store.NewActionEvidenceService(db)

	for _, test := range []struct {
		name   string
		mutate func(*store.ActionEvidence)
	}{
		{name: "no target device", mutate: func(r *store.ActionEvidence) { r.DeviceID = "" }},
		{name: "no workspace", mutate: func(r *store.ActionEvidence) { r.Workspace = "" }},
		{name: "no action identity", mutate: func(r *store.ActionEvidence) { r.AttemptID = "" }},
		{name: "a kind that is not a catalog entry", mutate: func(r *store.ActionEvidence) { r.Kind = action.Kind("shell") }},
		{name: "no disposition", mutate: func(r *store.ActionEvidence) { r.Disposition = "" }},
		{name: "an unknown disposition", mutate: func(r *store.ActionEvidence) { r.Disposition = store.EvidenceDisposition("overwritten") }},
		{name: "an unknown outcome", mutate: func(r *store.ActionEvidence) { r.Outcome = action.Outcome("probably_fine") }},
		{name: "an unknown postcondition state", mutate: func(r *store.ActionEvidence) { r.Postcondition = action.PostconditionState("maybe") }},
		{name: "a failure class outside the vocabulary", mutate: func(r *store.ActionEvidence) { r.FailureClass = domain.FailureClass("something else") }},
		{name: "a refusal reason that is free text", mutate: func(r *store.ActionEvidence) { r.RefusalReason = "the lease had expired by then" }},
		{name: "an observation handle that could hold content", mutate: func(r *store.ActionEvidence) { r.Observation.Token = "three words" }},
		{name: "an observation handle of unbounded length", mutate: func(r *store.ActionEvidence) { r.Observation.Token = strings.Repeat("h", 300) }},
		{name: "a foreground package that is not a package name", mutate: func(r *store.ActionEvidence) { r.Observation.ForegroundPackage = "com.example app" }},
		{name: "a negative addressed field length", mutate: func(r *store.ActionEvidence) { r.Observation.FieldLength = -1 }},
		{name: "an addressed field length beyond the typed text bound", mutate: func(r *store.ActionEvidence) { r.Observation.FieldLength = 1 << 21 }},
		{name: "a serial that is not an opaque token", mutate: func(r *store.ActionEvidence) { r.Serial = "emulator-1 && rm -rf" }},
		{name: "a record identity chosen by the caller", mutate: func(r *store.ActionEvidence) { r.ID = "attempt-1" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := actionEvidenceFixture(workspace, "attempt-1")
			test.mutate(&record)
			err := service.Append(ctx, record, "operator", "operator-1")
			if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
				t.Fatalf("append code = %v (err %v), want invalid_input", platformerrors.CodeOf(err), err)
			}
		})
	}

	stored, err := store.NewActionEvidenceRepository(db).ListForDevice(ctx, workspace, "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Fatalf("inadmissible records were persisted: %#v", stored)
	}
}
