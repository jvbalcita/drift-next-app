package main

import (
	"regexp"
	"strings"
	"testing"
)

// Regressions for what a bounded region does with the text it cannot draw.
//
// readability_test.go asserts the frame an operator reads against the defects
// the owner's frame showed. These assert the same contract at the edges that
// frame did not reach: a line wider than the frame, a region full of rows, and
// an event too long for the region that points the operator at the log view.
// The properties are asserted against the frame, for the same reason: a region
// that loses text silently and a region with nothing in it draw the same picture.

// regionText reassembles everything a bounded region drew, in order, with the
// row chrome removed: the left inset, the bullet and the continuation mark say
// where a row came from, not what it says. Nothing is trimmed, because a wrap
// keeps the space it breaks after and dropping it here would hide a lost
// character as a formatting choice.
func regionText(lines []string, title string) string {
	started := false
	var builder strings.Builder
	for _, line := range lines {
		if rule, isRule := ruleLine(line); isRule {
			if strings.HasPrefix(rule, title) {
				started = true
				continue
			}
			if started {
				break
			}
			continue
		}
		if !started {
			continue
		}
		row := strings.TrimPrefix(stripANSI(line), regionPrefix)
		row = strings.TrimPrefix(row, continuationMark+" ")
		row = strings.TrimPrefix(row, bulletMark)
		builder.WriteString(row)
	}
	return builder.String()
}

// ruleIndex is the line a section's rule was drawn on, or -1.
func ruleIndex(lines []string, title string) int {
	for index, line := range lines {
		if rule, isRule := ruleLine(line); isRule && strings.HasPrefix(rule, title) {
			return index
		}
	}
	return -1
}

// The log view is the surface the event region points at when an event does not
// fit, so it cannot be the surface that clips the same line: a pointer at a
// region that shortens the text sends the operator nowhere.
func TestLogViewCarriesALineLongerThanTheFrame(t *testing.T) {
	const long = "Finished `dev` profile [unoptimized + debuginfo] target(s) in 3.21s and wrote 12 artifacts into /Users/someone/a/very/long/path/that/keeps/going/target"
	for _, width := range []int{60, 100, 120} {
		state := viewState{mode: viewLogs, logs: []string{long}}
		lines := renderFrame(state, width, false)
		if visibleWidth(long) <= clampWidth(width) {
			t.Fatalf("width %d: this line is not wider than the frame, so the test proves nothing", width)
		}
		for index, line := range lines {
			if got := renderedWidth(line); got > clampWidth(width) {
				t.Fatalf("width %d: line %d is %d columns: %q", width, index, got, stripANSI(line))
			}
		}
		if got := regionText(lines, "Runtime log"); got != long {
			t.Fatalf("width %d: the log view shortened a line it had room to wrap:\n  frame: %q\n  line:  %q", width, got, long)
		}
	}
}

// A full event region pads nothing, so a separator that relied on the region's
// own padding would vanish exactly when the frame is busiest. The blank line is
// drawn, not inherited from whatever the region above happened to leave.
func TestActionsIsSeparatedFromAFullEventRegion(t *testing.T) {
	state := sampleState()
	state.events = nil
	for index := 0; index < eventRegionRows*3; index++ {
		state.events = append(state.events, "an event that filled a row")
	}
	lines := renderFrame(state, 100, false)

	events, actions := ruleIndex(lines, "Recent events"), ruleIndex(lines, "Actions")
	if events < 0 || actions < 0 {
		t.Fatalf("the frame is missing a section rule (events %d, actions %d)", events, actions)
	}
	// The region's own rows, then the separator: a region that grew a row for
	// every event it was sent would move the frame under the operator.
	if got, want := actions-events, eventRegionRows+2; got != want {
		t.Fatalf("the event region and the Actions rule are %d lines apart, want %d rows plus one separator", got, want)
	}
	if above := strings.TrimSpace(stripANSI(lines[actions-1])); above != "" {
		t.Fatalf("Actions starts immediately under %q: a full region runs into the action list", above)
	}
}

// When the region cannot show an event whole it says how many rows it withheld
// and where the whole text is. Both halves of that claim have to be true: a
// notice that points at a surface which does not hold the text is worse than no
// notice, because it ends the search.
func TestTheEventOverflowNoticeCountsRowsAndPointsAtTheWholeText(t *testing.T) {
	long := strings.Repeat("nine-char ", 80)
	state := sampleState()
	state.events = []string{long}
	lines := renderFrame(state, 100, false)

	notice := ""
	for _, line := range lines {
		plain := stripANSI(line)
		if strings.Contains(plain, continuationMark) && strings.Contains(plain, "more rows") {
			notice = plain
		}
	}
	if notice == "" {
		t.Fatalf("an event too long for the region was drawn without a notice:\n%s", strings.Join(lines, "\n"))
	}
	if !regexp.MustCompile(`[0-9]+ more rows`).MatchString(notice) {
		t.Fatalf("the notice does not say how much was withheld: %q", notice)
	}
	if !strings.Contains(notice, "9") {
		t.Fatalf("the notice does not name the key that opens the log view: %q", notice)
	}
	logLines := renderFrame(viewState{mode: viewLogs, logs: []string{long}}, 100, false)
	if got := regionText(logLines, "Runtime log"); !strings.Contains(got, long) {
		t.Fatalf("the notice sends the operator to the log view and the log view does not hold the event (%d columns carried of %d)",
			len(got), len(long))
	}
}

// The frame's height is a redraw guarantee in its own right: the painter walks
// back over the rows it drew, so a region whose height grows with its content is
// as broken as a line that grows past the frame width. Arriving content must not
// move the frame, and must not be able to push it off a normal terminal.
func TestBoundedRegionsHoldTheFrameHeightWhateverArrives(t *testing.T) {
	short := viewState{
		components: sampleState().components,
		events:     []string{"one event", "another event"},
		logs:       []string{"one log line", "another log line"},
	}
	long := viewState{
		components: sampleState().components,
		events:     []string{strings.Repeat("very long event content ", 300), strings.Repeat("界", 200)},
		logs:       []string{strings.Repeat("very long log content ", 300), strings.Repeat("界", 200)},
	}
	for _, mode := range []viewMode{viewDashboard, viewStatus, viewLogs} {
		for _, width := range []int{minFrameWidth, 40, 100, 240} {
			short.mode, long.mode = mode, mode
			quiet, busy := renderFrame(short, width, false), renderFrame(long, width, false)
			if len(quiet) != len(busy) {
				t.Fatalf("mode %d at width %d: a chatty source changed the frame height from %d rows to %d",
					mode, width, len(quiet), len(busy))
			}
			for index, line := range busy {
				if got := renderedWidth(line); got > clampWidth(width) {
					t.Fatalf("mode %d at width %d: line %d is %d columns: %q", mode, width, index, got, stripANSI(line))
				}
				if strings.ContainsRune(line, 0x1b) {
					t.Fatalf("mode %d at width %d emitted an escape sequence with colour off: %q", mode, width, line)
				}
			}
		}
	}
}
