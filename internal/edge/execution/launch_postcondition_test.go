package execution

import (
	"testing"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/adapter"
)

// A launch postcondition is decided on the package the device reported. ARC-101
// is what puts a value in that field in production, so these assertions pin what
// the evaluator does with a value, with a different value, and with none:
// verified, not verified, and indeterminate respectively. None of the three may
// be confused for another.
func TestALaunchPostconditionIsDecidedOnTheObservedPackage(t *testing.T) {
	spec := action.Specification{Postcondition: "the launched package is in the foreground"}
	intent := action.Intent{Kind: action.LaunchApp, ObservationToken: "sha256:before"}
	payload := InputPayload{Launch: &LaunchAppRequest{PackageName: "com.example.target"}}

	matched := mapObservationFor(payload, adapter.Observation{Token: "sha256:after", PackageName: "com.example.target"})
	if state, class, outcome := evaluatePostcondition(spec, intent, payload, matched); outcome != action.OutcomeVerified {
		t.Fatalf("matching package: outcome = %q class = %q state = %q, want verified", outcome, class, state)
	}

	mismatched := mapObservationFor(payload, adapter.Observation{Token: "sha256:after", PackageName: "com.somewhere.else"})
	state, class, outcome := evaluatePostcondition(spec, intent, payload, mismatched)
	if outcome == action.OutcomeVerified {
		t.Fatalf("mismatching package: outcome = %q class = %q state = %q, want it never verified", outcome, class, state)
	}
	if outcome != action.OutcomeFailed || state != action.PostconditionFailed {
		t.Fatalf("mismatching package: outcome = %q state = %q, want a failed postcondition", outcome, state)
	}

	unread := mapObservationFor(payload, adapter.Observation{Token: "sha256:after"})
	if !unread.Partial {
		t.Fatal("an observation that reported no package must be partial, or a launch nobody observed would be failed")
	}
	if state, class, outcome := evaluatePostcondition(spec, intent, payload, unread); outcome != action.OutcomeIndeterminate {
		t.Fatalf("unread package: outcome = %q class = %q state = %q, want indeterminate", outcome, class, state)
	}
}
