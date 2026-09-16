package execution_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/execution"
	store "drift.local/drift-next/internal/store/sqlite"
)

// This file is the integration proof for the action evidence boundary (ARC-64):
// the records are appended to a disposable SQLite database through the real
// append-only service, with the real kernel and a fake transport. No device and
// no adb process is involved.

// storedEvidenceRendering reads the evidence back through the repository and
// renders every field of every record, so a test can assert what was persisted
// rather than what was handed over.
func storedEvidenceRendering(t *testing.T, fixture sqliteInputFixture) string {
	t.Helper()
	records, err := store.NewActionEvidenceRepository(fixture.db).ListForDevice(context.Background(), fixture.workspace, string(fixture.device))
	if err != nil {
		t.Fatalf("read the stored evidence: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("no evidence was stored")
	}
	rendered := ""
	for _, record := range records {
		rendered += fmt.Sprintf("%#v|", record)
	}
	return rendered
}

// TestActionEvidenceIsAppendOnlyAcrossDispatchesAgainstSQLite proves the card's
// second property end to end: a second dispatch of the same action appends its
// own record, the first record is byte-identical afterwards, and a duplicate
// delivery is recorded as the replay it is rather than as a second dispatch.
func TestActionEvidenceIsAppendOnlyAcrossDispatchesAgainstSQLite(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLiteInputFixture(t, time.Minute, adb.StateDevice)
	repository := store.NewActionEvidenceRepository(fixture.db)

	first := fixture.request("attempt-evidence", "evidence-key")
	if _, err := fixture.dispatcher.Run(ctx, first, "operator", "operator-1"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	before, err := repository.ListForAttempt(ctx, fixture.workspace, first.IntentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 {
		t.Fatalf("evidence for one dispatch = %d records, want 1", len(before))
	}
	if before[0].Disposition != store.EvidenceDispatched || before[0].Outcome != action.OutcomeVerified || before[0].Observation.Token != postToken {
		t.Fatalf("first evidence = %#v, want the dispatched outcome with the fresh observation", before[0])
	}
	if before[0].Kind != action.Tap || before[0].DeviceID != string(fixture.device) || before[0].AttemptID != first.IntentID {
		t.Fatalf("first evidence identity = %#v, want the dispatched tap on %q", before[0], fixture.device)
	}
	if fixture.transport.invocationCount() != 1 {
		t.Fatalf("device calls = %d, want 1", fixture.transport.invocationCount())
	}

	// The same action dispatched again, under its own identity: a second record.
	retry := fixture.request("attempt-evidence-retry", "evidence-key-retry")
	if _, err := fixture.dispatcher.Run(ctx, retry, "operator", "operator-1"); err != nil {
		t.Fatalf("second dispatch: %v", err)
	}

	// A duplicate delivery of the first attempt: recorded as a replay, and it
	// still does not act on the device.
	duplicate := fixture.request("attempt-evidence-duplicate", "evidence-key")
	if _, err := fixture.dispatcher.Run(ctx, duplicate, "operator", "operator-1"); err != nil {
		t.Fatalf("duplicate delivery: %v", err)
	}
	if fixture.transport.invocationCount() != 2 {
		t.Fatalf("device calls = %d, want 2: a duplicate delivery must not act again", fixture.transport.invocationCount())
	}

	after, err := repository.ListForAttempt(ctx, fixture.workspace, first.IntentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 {
		t.Fatalf("evidence for the first attempt = %d records, want the one it started with", len(after))
	}
	if !reflect.DeepEqual(before[0], after[0]) {
		t.Fatalf("a later dispatch rewrote the first record:\n before %#v\n after  %#v", before[0], after[0])
	}

	history, err := repository.ListForDevice(ctx, fixture.workspace, string(fixture.device))
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("device evidence = %d records, want 3 appended records", len(history))
	}
	// The fixture's clock is frozen, so all three records carry the same
	// timestamp. The history is still read in the order it was appended, which
	// is the point: append order is not the recorded time.
	if history[0].AttemptID != first.IntentID || history[1].AttemptID != retry.IntentID {
		t.Fatalf("device evidence order = (%q, %q, %q), want the append order", history[0].AttemptID, history[1].AttemptID, history[2].AttemptID)
	}
	if history[2].Disposition != store.EvidenceReplayed || history[2].Observation.Token != "" {
		t.Fatalf("replay evidence = %#v, want a replay that copied no observation", history[2])
	}
}

// TestTheStoredEvidenceOfTypedTextCarriesNeitherTheHandleNorTheValue asserts the
// redaction rule where it matters most: against the rows a database actually
// holds. The value reaches the device because it is the input; no stored field
// carries the value or the handle that names it, and the addressed field is
// recorded as a count.
func TestTheStoredEvidenceOfTypedTextCarriesNeitherTheHandleNorTheValue(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLiteInputFixture(t, time.Minute, adb.StateDevice)
	fixture.observer.observation = execution.PostconditionObservation{Token: postToken, FieldLength: len(typedValueFixture)}

	payload := textPayload()
	request := fixture.request("attempt-evidence-text", "evidence-key-text")
	request.Payload = payload
	request.Target = action.SemanticTarget{ResourceID: "composer"}
	if _, err := fixture.dispatcher.Run(ctx, request, "operator", "operator-1"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if fixture.transport.invocationCount() != 1 {
		t.Fatalf("device calls = %d, want 1", fixture.transport.invocationCount())
	}
	matchArgs(t, fixture.transport.invocation(0).args, "shell", "input", "text", typedValueFixture)

	stored := storedEvidenceRendering(t, fixture)
	if strings.Contains(stored, typedValueFixture) {
		t.Fatalf("the stored evidence carries the typed value: %s", stored)
	}
	if strings.Contains(stored, payload.Text.Handle) {
		t.Fatalf("the stored evidence carries the handle to the typed value: %s", stored)
	}
	if !strings.Contains(stored, string(action.TextInput)) {
		t.Fatalf("stored evidence = %s, want the typed text action recorded by kind", stored)
	}
	if !strings.Contains(stored, fmt.Sprintf("FieldLength:%d", len(typedValueFixture))) {
		t.Fatalf("stored evidence = %s, want the addressed field recorded as a count", stored)
	}
}
