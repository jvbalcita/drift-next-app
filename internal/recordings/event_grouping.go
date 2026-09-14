package recordings

import (
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/action"
)

const (
	defaultDoubleTapWindow  = 300 * time.Millisecond
	defaultTextWindow       = 750 * time.Millisecond
	defaultLongPressMinimum = 600 * time.Millisecond
)

// GroupingOptions controls deterministic input grouping. The input callback
// never sleeps; these windows are evaluated by the recorder worker.
type GroupingOptions struct {
	DoubleTapWindow  time.Duration
	TextWindow       time.Duration
	LongPressMinimum time.Duration
}

func (o GroupingOptions) normalized() GroupingOptions {
	if o.DoubleTapWindow <= 0 {
		o.DoubleTapWindow = defaultDoubleTapWindow
	}
	if o.TextWindow <= 0 {
		o.TextWindow = defaultTextWindow
	}
	if o.LongPressMinimum <= 0 {
		o.LongPressMinimum = defaultLongPressMinimum
	}
	return o
}

// GroupedAction is an in-memory logical action. It is normalized and redacted
// before it can cross the persistence boundary.
type GroupedAction struct {
	DeviceID              string
	Kind                  action.Kind
	StartedAt             time.Time
	FinishedAt            time.Time
	Text                  string
	ValueLength           int
	Sensitivity           Sensitivity
	Target                TargetMetadata
	Start                 action.Coordinate
	End                   *action.Coordinate
	KeyCode               int
	IrreversibleConfirmed bool
}

type EventGrouper struct {
	options GroupingOptions
	pending *GroupedAction
	device  string
}

func NewEventGrouper(options GroupingOptions) *EventGrouper {
	return &EventGrouper{options: options.normalized()}
}

// Add returns actions that are no longer eligible for grouping. The final
// buffered action is returned by Flush.
func (g *EventGrouper) Add(input RawInput) ([]GroupedAction, error) {
	if g == nil {
		return nil, fmt.Errorf("event grouper is required")
	}
	if input.Sensitivity == "" {
		input.Sensitivity = SensitivityNone
	}
	if err := input.Validate(); err != nil {
		return nil, err
	}
	if g.device != "" && g.device != string(input.DeviceID) {
		return nil, fmt.Errorf("event grouper cannot combine devices")
	}
	g.device = string(input.DeviceID)
	current := groupedAction(input, g.options)

	if g.pending == nil {
		if canBuffer(current) {
			g.pending = &current
			return nil, nil
		}
		return []GroupedAction{current}, nil
	}

	if canCombine(*g.pending, current, g.options) {
		combine(g.pending, current)
		return nil, nil
	}

	ready := []GroupedAction{*g.pending}
	g.pending = nil
	if canBuffer(current) {
		g.pending = &current
		return ready, nil
	}
	return append(ready, current), nil
}

func (g *EventGrouper) Flush() ([]GroupedAction, error) {
	if g == nil {
		return nil, fmt.Errorf("event grouper is required")
	}
	if g.pending == nil {
		return nil, nil
	}
	result := []GroupedAction{*g.pending}
	g.pending = nil
	return result, nil
}

func canBuffer(input GroupedAction) bool {
	return input.Kind == action.Tap || input.Kind == action.TextInput || input.Kind == action.TextDelete
}

func canCombine(previous, current GroupedAction, options GroupingOptions) bool {
	if previous.DeviceID != current.DeviceID || previous.Target != current.Target {
		return false
	}
	if current.StartedAt.Before(previous.StartedAt) {
		return false
	}
	switch {
	case previous.Kind == action.Tap && current.Kind == action.Tap:
		return current.StartedAt.Sub(previous.StartedAt) <= options.DoubleTapWindow
	case previous.Kind == action.TextInput && current.Kind == action.TextInput:
		return current.StartedAt.Sub(previous.FinishedAt) <= options.TextWindow
	case previous.Kind == action.TextDelete && current.Kind == action.TextDelete:
		return current.StartedAt.Sub(previous.FinishedAt) <= options.TextWindow
	default:
		return false
	}
}

func combine(previous *GroupedAction, current GroupedAction) {
	if previous.Kind == action.Tap && current.Kind == action.Tap {
		previous.Kind = action.DoubleTap
		previous.FinishedAt = current.FinishedAt
		return
	}
	previous.Text += current.Text
	previous.ValueLength += current.ValueLength
	previous.FinishedAt = current.FinishedAt
	previous.Sensitivity = combineSensitivity(previous.Sensitivity, current.Sensitivity)
	if current.IrreversibleConfirmed {
		previous.IrreversibleConfirmed = true
	}
}

func combineSensitivity(left, right Sensitivity) Sensitivity {
	if left == SensitivityUncertain || right == SensitivityUncertain {
		return SensitivityUncertain
	}
	if left == SensitivitySensitive || right == SensitivitySensitive {
		return SensitivitySensitive
	}
	return SensitivityNone
}

func groupedAction(input RawInput, options GroupingOptions) GroupedAction {
	finished := input.EndAt
	if finished.IsZero() {
		finished = input.At
	}
	valueLength := input.ValueLength
	if valueLength == 0 && input.Text != "" {
		valueLength = len(input.Text)
	}
	kind := inputKind(input.Kind)
	if input.Kind == InputTap && finished.Sub(input.At) >= options.LongPressMinimum {
		kind = action.LongPress
	}
	return GroupedAction{
		DeviceID:              string(input.DeviceID),
		Kind:                  kind,
		StartedAt:             input.At.UTC(),
		FinishedAt:            finished.UTC(),
		Text:                  input.Text,
		ValueLength:           valueLength,
		Sensitivity:           input.Sensitivity,
		Target:                input.Target,
		Start:                 input.Start,
		End:                   cloneCoordinate(input.End),
		KeyCode:               input.KeyCode,
		IrreversibleConfirmed: input.IrreversibleConfirmed,
	}
}

func inputKind(kind RawInputKind) action.Kind {
	switch kind {
	case InputTap:
		return action.Tap
	case InputText:
		return action.TextInput
	case InputTextDelete:
		return action.TextDelete
	case InputClear:
		return action.Clear
	case InputSwipe:
		return action.Swipe
	case InputScroll:
		return action.Scroll
	case InputDrag:
		return action.Drag
	case InputBack:
		return action.Back
	case InputHome:
		return action.Home
	case InputEnter:
		return action.Enter
	case InputKey:
		return action.KeyEvent
	case InputUIChange:
		return action.UIChange
	case InputStateChange:
		return action.StateChange
	default:
		return action.Kind(strings.TrimSpace(string(kind)))
	}
}

func cloneCoordinate(input *action.Coordinate) *action.Coordinate {
	if input == nil {
		return nil
	}
	result := *input
	return &result
}
