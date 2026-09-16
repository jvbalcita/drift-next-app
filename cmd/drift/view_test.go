package main

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/runtime"
)

var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

func stripANSI(text string) string { return ansiEscape.ReplaceAllString(text, "") }

func renderedWidth(text string) int { return len([]rune(stripANSI(text))) }

func sampleState() viewState {
	return viewState{
		components: []runtime.ComponentStatus{
			{Name: "Control Plane", State: "ready", Detail: "Ready (already running)"},
			{Name: "Device Service", State: "starting", Detail: "Process started; waiting for readiness"},
			{Name: "Desktop Application", State: "failed", Detail: "pnpm is unavailable"},
		},
		events:    []string{"ADB discovered and validated", "All requested components are ready"},
		logs:      []string{"Running pnpm typecheck", "Running go test ./..."},
		mode:      viewDashboard,
		refreshed: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC),
	}
}

func frameText(state viewState, width int, color bool) string {
	return strings.Join(renderFrame(state, width, color), "\n")
}

func TestFrameFitsNarrowAndWideTerminals(t *testing.T) {
	for _, width := range []int{minFrameWidth, 32, 48, 67, 100, maxFrameWidth, 240} {
		limit := clampWidth(width)
		for _, mode := range []viewMode{viewDashboard, viewStatus, viewLogs} {
			state := sampleState()
			state.mode = mode
			state.input = "16"
			for number, line := range renderFrame(state, width, true) {
				if got := renderedWidth(line); got > limit {
					t.Fatalf("renderFrame(width=%d, mode=%d) line %d is %d columns: %q", width, mode, number, got, line)
				}
			}
		}
	}
}

func TestFrameUsesFallbackWidthWhenSizeIsUnknown(t *testing.T) {
	for _, width := range []int{0, -1} {
		widest := 0
		for _, line := range renderFrame(sampleState(), width, false) {
			if got := renderedWidth(line); got > widest {
				widest = got
			}
		}
		if widest != fallbackFrameWidth {
			t.Fatalf("width %d: expected the %d-column fallback frame, widest line was %d", width, fallbackFrameWidth, widest)
		}
	}
}

func TestFrameWithColourDisabledContainsNoEscapeSequences(t *testing.T) {
	for _, width := range []int{0, minFrameWidth, 44, 100, 300} {
		for _, mode := range []viewMode{viewDashboard, viewStatus, viewLogs} {
			for _, state := range []viewState{
				func() viewState {
					state := sampleState()
					state.mode = mode
					state.input = "12"
					state.busy = true
					state.busyLabel = "Run All Checks"
					state.busyElapsed = 1500 * time.Millisecond
					return state
				}(),
				func() viewState {
					state := sampleState()
					state.mode = mode
					state.notice = "Setup Environment completed in 210ms"
					state.noticeLevel = noticeOK
					return state
				}(),
				func() viewState {
					state := sampleState()
					state.mode = mode
					state.notice = "Start All failed after 15s: readiness timeout"
					state.noticeLevel = noticeFail
					return state
				}(),
			} {
				for _, line := range renderFrame(state, width, false) {
					if strings.ContainsRune(line, 0x1b) {
						t.Fatalf("renderFrame(width=%d, mode=%d) emitted an escape sequence: %q", width, mode, line)
					}
				}
			}
		}
	}
}

func TestFrameWithColourEnabledClosesEveryStyle(t *testing.T) {
	text := frameText(sampleState(), 100, true)
	escapes := ansiEscape.FindAllString(text, -1)
	resets := 0
	for _, escape := range escapes {
		if escape == sgrReset {
			resets++
		}
	}
	if resets == 0 {
		t.Fatal("expected styled segments when colour is enabled")
	}
	if len(escapes) != 2*resets {
		t.Fatalf("style segments are unbalanced: %d escapes, %d of them resets", len(escapes), resets)
	}
}

func TestLogViewRendersOnlyTheMostRecentLines(t *testing.T) {
	state := sampleState()
	state.mode = viewLogs
	state.logs = nil
	for index := 0; index < 200; index++ {
		state.logs = append(state.logs, fmt.Sprintf("log-line-%03d", index))
	}
	text := frameText(state, 100, false)
	if !strings.Contains(text, "log-line-199") {
		t.Fatal("most recent log line is missing from the log view")
	}
	oldest := 200 - logRegionRows
	if strings.Contains(text, fmt.Sprintf("log-line-%03d", oldest-1)) {
		t.Fatalf("log view rendered a line older than the most recent %d", logRegionRows)
	}
	first, last := strings.Index(text, fmt.Sprintf("log-line-%03d", oldest)), strings.Index(text, "log-line-199")
	if first < 0 || last < 0 || first > last {
		t.Fatalf("log window ordering is wrong: first=%d last=%d", first, last)
	}
	if count := strings.Count(text, "log-line-"); count != logRegionRows {
		t.Fatalf("log view rendered %d lines, want a %d-line window", count, logRegionRows)
	}
}

func TestTailWindowKeepsMostRecentLinesInOrderWithoutMutatingInput(t *testing.T) {
	lines := []string{"one", "two", "three", "four"}
	got := tailWindow(lines, 2)
	if want := []string{"three", "four"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tailWindow(%v, 2) = %v, want %v", lines, got, want)
	}
	if want := []string{"one", "two", "three", "four"}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("tailWindow mutated its input: %v", lines)
	}
	if got := tailWindow(lines, 0); got != nil {
		t.Fatalf("tailWindow(lines, 0) = %v, want nil", got)
	}
	if got := tailWindow(lines, 9); !reflect.DeepEqual(got, lines) {
		t.Fatalf("tailWindow wider than the input changed it: %v", got)
	}
}

func TestMenuAdvertisesEveryOfferedKey(t *testing.T) {
	items := menu()
	lo, hi := keyRange(items)
	label := promptLabel(items)
	if !strings.Contains(label, fmt.Sprintf("%d-%d", lo, hi)) {
		t.Fatalf("prompt %q does not advertise the %d-%d key range", label, lo, hi)
	}
	offered := map[string]bool{}
	for _, item := range items {
		if item.Label == "" {
			t.Fatalf("menu item %q has no label", item.Key)
		}
		if offered[item.Key] {
			t.Fatalf("menu key %q is offered twice", item.Key)
		}
		offered[item.Key] = true
		number, err := strconv.Atoi(item.Key)
		if err == nil {
			if number < lo || number > hi {
				t.Fatalf("menu key %q is outside the advertised range %d-%d", item.Key, lo, hi)
			}
			continue
		}
		if !strings.Contains(label, item.Key) {
			t.Fatalf("shortcut %q is not advertised in prompt %q", item.Key, label)
		}
	}
	for number := lo; number <= hi; number++ {
		if !offered[strconv.Itoa(number)] {
			t.Fatalf("prompt advertises key %d but no menu action offers it", number)
		}
	}
	for _, item := range items {
		resolved, ok := lookupItem(items, item.Key)
		if !ok || resolved.Key != item.Key {
			t.Fatalf("menu key %q does not resolve to its action", item.Key)
		}
	}
	if _, ok := lookupItem(items, "99"); ok {
		t.Fatal("an unknown key resolved to a menu action")
	}
}

func TestMenuRendersEveryOfferedAction(t *testing.T) {
	text := frameText(viewState{mode: viewDashboard}, 120, false)
	for _, item := range menu() {
		if item.Shortcut {
			if !strings.Contains(text, item.Key) {
				t.Fatalf("shortcut %q is not rendered anywhere in the frame", item.Key)
			}
			continue
		}
		if !strings.Contains(text, item.Key+" "+item.Label) {
			t.Fatalf("menu cell %q (%s) is not rendered in the frame", item.Key, item.Label)
		}
	}
}
