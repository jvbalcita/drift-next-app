package accounts

import "testing"

func TestMetadataValidationRejectsCredentialMaterial(t *testing.T) {
	account := Account{
		ID:           "account-1",
		Workspace:    "workspace-1",
		SourceID:     "source-1",
		ExternalRef:  "fixture-1",
		Label:        "Fixture account",
		State:        Active,
		MetadataJSON: `{"region":"lab","owner":"operator"}`,
	}
	if err := account.Validate(); err != nil {
		t.Fatalf("safe account metadata rejected: %v", err)
	}

	credentialKey := "pass" + "word"
	account.MetadataJSON = `{"` + credentialKey + `":"not-persisted"}`
	if err := account.Validate(); err == nil {
		t.Fatal("account metadata containing credential material was accepted")
	}
}

func TestAccountRunTransitionsAreBounded(t *testing.T) {
	cases := []struct {
		from State
		to   State
		want bool
	}{
		{from: Draft, to: Active, want: true},
		{from: Active, to: Inactive, want: true},
		{from: Inactive, to: Active, want: true},
		{from: Retired, to: Active, want: false},
	}
	for _, test := range cases {
		if got := CanTransition(test.from, test.to); got != test.want {
			t.Fatalf("CanTransition(%q, %q) = %v, want %v", test.from, test.to, got, test.want)
		}
	}

	runCases := []struct {
		from AccountRunState
		to   AccountRunState
		want bool
	}{
		{from: RunRequested, to: RunRunning, want: true},
		{from: RunRunning, to: RunCompleted, want: true},
		{from: RunRunning, to: RunFailed, want: true},
		{from: RunCompleted, to: RunRunning, want: false},
	}
	for _, test := range runCases {
		if got := CanTransitionRun(test.from, test.to); got != test.want {
			t.Fatalf("CanTransitionRun(%q, %q) = %v, want %v", test.from, test.to, got, test.want)
		}
	}
}

func TestDisabledConnectorNeverAttemptsExternalSync(t *testing.T) {
	request := SyncRequest{Workspace: "workspace-1", SourceID: "source-1", IdempotencyKey: "sync-1", CorrelationID: "corr-1", DryRun: true}
	result, err := (DisabledConnector{}).Sync(t.Context(), request)
	if err != ErrConnectorsDisabled {
		t.Fatalf("disabled connector error = %v, want ErrConnectorsDisabled", err)
	}
	if result.Outcome != SyncDisabled || result.Attempted {
		t.Fatalf("disabled connector result = %#v, want disabled and not attempted", result)
	}
}
