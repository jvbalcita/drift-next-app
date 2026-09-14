package settings

import "testing"

func TestSafetyCriticalSettingsUseTypedValidation(t *testing.T) {
	valid := []struct {
		key   string
		value string
	}{
		{key: "require_explicit_approval", value: "true"},
		{key: "max_action_timeout_ms", value: "300000"},
		{key: "event_retention_days", value: "30"},
		{key: "table_density", value: `"comfortable"`},
	}
	for _, test := range valid {
		if err := ValidateValue(test.key, test.value); err != nil {
			t.Errorf("ValidateValue(%q, %q) error = %v", test.key, test.value, err)
		}
	}

	invalid := []struct {
		key   string
		value string
	}{
		{key: "require_explicit_approval", value: `"yes"`},
		{key: "max_action_timeout_ms", value: "300001"},
		{key: "event_retention_days", value: "null"},
		{key: "table_density", value: `"unbounded"`},
		{key: "operator_notes", value: `{"pass` + `word":"not-persisted"}`},
	}
	for _, test := range invalid {
		if err := ValidateValue(test.key, test.value); err == nil {
			t.Errorf("ValidateValue(%q, %q) accepted unsafe or invalid value", test.key, test.value)
		}
	}
}

func TestSettingValidationSeparatesWorkspaceTargetScope(t *testing.T) {
	setting := Setting{ID: "setting-1", Workspace: "workspace-1", Scope: ScopeWorkspace, Key: "event_retention_days", ValueJSON: "30", State: Active}
	if err := setting.Validate(); err != nil {
		t.Fatalf("workspace setting rejected: %v", err)
	}
	setting.TargetID = "device-1"
	if err := setting.Validate(); err == nil {
		t.Fatal("workspace setting accepted a target ID")
	}
}
