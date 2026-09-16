package execution_test

import (
	"reflect"
	"strings"
	"testing"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/execution"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

// This file is the redaction half of the action evidence boundary (ARC-64). It
// asserts the rule AGENTS.md section 9 states - never put text content in a
// message field, and reference a value by an opaque, pattern-checked handle -
// against the record the dispatcher hands to the append-only store. No device,
// no adb process and no credential is involved.

func evidenceRecordFixture() store.ActionEvidence {
	return store.ActionEvidence{
		Workspace:         inputWorkspace,
		DeviceID:          inputDevice,
		Serial:            inputSerial,
		AttemptID:         "attempt-evidence",
		Kind:              action.Tap,
		InvocationSurface: action.SurfaceManual,
		Disposition:       store.EvidenceDispatched,
		Outcome:           action.OutcomeVerified,
		Postcondition:     action.PostconditionPassed,
		Observation:       store.ActionObservation{Token: postToken, ForegroundPackage: "com.example.app", FieldLength: 4},
	}
}

// TestTheEvidenceRecordHasNoFieldForTypedContentOrACredential is the structural
// half of the rule: a record cannot leak content it has nowhere to carry. The
// walk is asserted to have seen the whole record, so the test cannot pass by
// walking nothing.
func TestTheEvidenceRecordHasNoFieldForTypedContentOrACredential(t *testing.T) {
	var names []string
	var walk func(prefix string, typ reflect.Type)
	walk = func(prefix string, typ reflect.Type) {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			path := prefix + field.Name
			if field.Type.Kind() == reflect.Struct {
				walk(path+".", field.Type)
				continue
			}
			names = append(names, path)
		}
	}
	walk("", reflect.TypeOf(store.ActionEvidence{}))
	if len(names) < 15 {
		t.Fatalf("the evidence record has %d fields (%v), want the whole record walked", len(names), names)
	}
	for _, name := range names {
		lower := strings.ToLower(name)
		for _, banned := range []string{"text", "content", "handle", "value", "clipboard", "password", "passphrase", "credential", "secret", "bearer", "dsn"} {
			if strings.Contains(lower, banned) {
				t.Errorf("evidence field %q could carry %s, which the redaction rule forbids", name, banned)
			}
		}
	}
}

// TestEveryStringEvidenceFieldIsAdmittedOnlyAsAPattern walks the record the way
// a reviewer would: it sets each string field to a value that looks like typed
// content and asserts the redaction refuses the record rather than persisting
// it. Fail closed is deliberate - blanking the field would store a record that
// looks like nothing was observed when the observation was in fact inadmissible.
func TestEveryStringEvidenceFieldIsAdmittedOnlyAsAPattern(t *testing.T) {
	record := evidenceRecordFixture()
	value := reflect.ValueOf(&record).Elem()
	admitted := 0
	var walk func(prefix string, value reflect.Value)
	walk = func(prefix string, value reflect.Value) {
		typ := value.Type()
		for i := 0; i < typ.NumField(); i++ {
			field := value.Field(i)
			path := prefix + typ.Field(i).Name
			switch field.Kind() {
			case reflect.String:
				original := field.String()
				field.SetString("typed content here")
				if _, err := execution.RedactActionEvidence(record); err == nil {
					t.Errorf("evidence field %q admitted a value that could be typed content", path)
				}
				field.SetString(original)
				admitted++
			case reflect.Struct:
				walk(path+".", field)
			}
		}
	}
	walk("", value)
	if admitted < 10 {
		t.Fatalf("the redaction walk covered %d string fields, want every string field of the record", admitted)
	}
	if _, err := execution.RedactActionEvidence(record); err != nil {
		t.Fatalf("the admissible fixture was refused: %v", err)
	}
}

func TestRedactionRefusesARecordThatIsNotAdmissibleEvidence(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*store.ActionEvidence)
	}{
		{name: "no target device", mutate: func(r *store.ActionEvidence) { r.DeviceID = "" }},
		{name: "no workspace", mutate: func(r *store.ActionEvidence) { r.Workspace = "" }},
		{name: "an action identity that is free text", mutate: func(r *store.ActionEvidence) { r.AttemptID = "the second attempt" }},
		{name: "a kind that is not a catalog entry", mutate: func(r *store.ActionEvidence) { r.Kind = action.Kind("shell") }},
		{name: "an unknown disposition", mutate: func(r *store.ActionEvidence) { r.Disposition = store.EvidenceDisposition("retried") }},
		{name: "an unknown outcome", mutate: func(r *store.ActionEvidence) { r.Outcome = action.Outcome("probably_fine") }},
		{name: "an unknown postcondition state", mutate: func(r *store.ActionEvidence) { r.Postcondition = action.PostconditionState("maybe") }},
		{name: "a failure class outside the shared vocabulary", mutate: func(r *store.ActionEvidence) { r.FailureClass = domain.FailureClass("unknown_screen_2") }},
		{name: "an observation failure class outside the shared vocabulary", mutate: func(r *store.ActionEvidence) { r.Observation.FailureClass = domain.FailureClass("blurred") }},
		{name: "a refusal reason outside the dispatch vocabulary", mutate: func(r *store.ActionEvidence) { r.RefusalReason = "lease_expired_or_revoked" }},
		{name: "a refusal reason that is free text", mutate: func(r *store.ActionEvidence) { r.RefusalReason = "the lease was gone" }},
		{name: "a transport serial carrying a command", mutate: func(r *store.ActionEvidence) { r.Serial = "mock-device-alpha && id" }},
		{name: "an observation handle carrying content", mutate: func(r *store.ActionEvidence) { r.Observation.Token = "typed content" }},
		{name: "a foreground package that is not a package name", mutate: func(r *store.ActionEvidence) { r.Observation.ForegroundPackage = "com.example app" }},
		{name: "a negative addressed field length", mutate: func(r *store.ActionEvidence) { r.Observation.FieldLength = -1 }},
		{name: "an addressed field length beyond the typed text bound", mutate: func(r *store.ActionEvidence) { r.Observation.FieldLength = 1 << 21 }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			record := evidenceRecordFixture()
			test.mutate(&record)
			_, err := execution.RedactActionEvidence(record)
			if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
				t.Fatalf("redaction code = %v (err %v), want invalid_input", platformerrors.CodeOf(err), err)
			}
		})
	}
}

// TestRedactionKeepsAnAdmissibleRecordIntact proves redaction is not a
// transformation of what was observed: an admissible record is handed on
// unchanged, so the evidence a recording reads is the evidence the dispatch
// produced.
func TestRedactionKeepsAnAdmissibleRecordIntact(t *testing.T) {
	record := evidenceRecordFixture()
	redacted, err := execution.RedactActionEvidence(record)
	if err != nil {
		t.Fatalf("redaction: %v", err)
	}
	if !reflect.DeepEqual(record, redacted) {
		t.Fatalf("redaction changed an admissible record:\n before %#v\n after  %#v", record, redacted)
	}
}
