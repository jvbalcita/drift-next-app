package recordings

import (
	"fmt"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/platform/redaction"
)

const (
	DefaultTimeoutMillis = int64(30 * 1000)
	DefaultPostcondition = "after_observation_captured"
)

// NormalizeAction converts a grouped callback action into the typed action
// metadata used by the recorder and skill compiler.
func NormalizeAction(input GroupedAction) (LogicalAction, error) {
	if _, ok := action.Lookup(input.Kind); !ok {
		return LogicalAction{}, fmt.Errorf("grouped action %q is not allow-listed", input.Kind)
	}
	sensitivity := input.Sensitivity
	if sensitivity == "" {
		sensitivity = SensitivityNone
	}
	if sensitivity == SensitivityNone && sensitiveTarget(input.Target) {
		sensitivity = SensitivityUncertain
	}
	display := input.Text
	if input.Kind != action.TextInput {
		display = ""
	}
	if sensitivity != SensitivityNone {
		display = redaction.Replacement
	}
	actionValue := LogicalAction{
		Kind:                  input.Kind,
		Target:                input.Target,
		DisplayValue:          display,
		ValueLength:           input.ValueLength,
		Sensitivity:           sensitivity,
		KeyCode:               input.KeyCode,
		IrreversibleConfirmed: input.IrreversibleConfirmed,
		TimeoutMillis:         DefaultTimeoutMillis,
		Postcondition:         DefaultPostcondition,
	}
	if actionValue.ValueLength == 0 && input.Text != "" {
		actionValue.ValueLength = len(input.Text)
	}
	if input.Start.Space != "" {
		coordinate := input.Start
		actionValue.Coordinate = &coordinate
	}
	if input.End != nil {
		actionValue.Gesture = &Gesture{Points: []action.Coordinate{input.Start, *input.End}, DurationMs: input.FinishedAt.Sub(input.StartedAt).Milliseconds()}
	}
	if err := actionValue.Validate(); err != nil {
		return LogicalAction{}, err
	}
	return actionValue, nil
}
