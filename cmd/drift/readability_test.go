package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"drift.local/drift-next/internal/runtime"
)

// The readability contract, asserted against the rendered frame rather than
// against the data that feeds it. Every defect these tests cover was visible in
// one live frame and invisible in the unit tests that came before, so they read
// the frame the way an operator does: as lines of text.

// actionCell is one numbered action as drawn, with the position it occupies so
// reading order can be asserted instead of assumed. mark is the glyph that
// distinguishes an action that changes processes from a read-only one.
type actionCell struct {
	row   int
	col   int
	key   string
	label string
	mark  string
}

// cellStart finds where an action cell begins: an optional classification
// glyph, the key, and the space after it. Cell labels are sliced between one
// match and the next, because Go's regexp engine has no lookahead.
var cellStart = regexp.MustCompile(`([▸◦])?\s*([0-9]{1,2}) `)

// frameActionCells extracts every numbered action cell, in the order the
// terminal draws them: left to right within a line, then the next line. Lines
// that are chrome - rules, the prompt, and the key legend - are skipped, so a
// key that is only ever mentioned in prose is not mistaken for an action. A
// candidate is only a cell if it starts the line or follows the grid's gutter,
// so a number inside a sentence is not a key.
func frameActionCells(lines []string) []actionCell {
	cells := make([]actionCell, 0, 32)
	for row, line := range lines {
		plain := stripANSI(line)
		if _, isRule := ruleLine(line); isRule || isChromeLine(plain) {
			continue
		}
		matches := cellStart.FindAllStringSubmatchIndex(plain, -1)
		for col, match := range matches {
			// The match can begin at the whitespace before a glyph, so the
			// gutter is measured from the key itself.
			if !atCellStart(plain, match[4]) {
				continue
			}
			labelEnd := len(plain)
			if col+1 < len(matches) {
				labelEnd = matches[col+1][0]
			}
			mark := ""
			if match[2] >= 0 {
				mark = plain[match[2]:match[3]]
			}
			cells = append(cells, actionCell{
				row:   row,
				col:   col,
				mark:  mark,
				key:   plain[match[4]:match[5]],
				label: strings.TrimRight(plain[match[1]:labelEnd], " "),
			})
		}
	}
	return cells
}

// atCellStart reports whether an offset begins a cell: at the line's start
// (after the gutter) or after the padding that separates two cells.
func atCellStart(line string, offset int) bool {
	start := offset
	for start > 0 && line[start-1] == ' ' {
		start--
	}
	if start == 0 {
		return true
	}
	return offset-start >= 2
}

// isChromeLine reports whether a line explains keys rather than offering them.
func isChromeLine(plain string) bool {
	trimmed := strings.TrimSpace(plain)
	if strings.HasPrefix(trimmed, "Select an action") || strings.HasPrefix(trimmed, "Keys") {
		return true
	}
	// The prompt sits immediately above the legend; both name keys in prose.
	for _, marker := range []string{"· 0 ", "0 overview"} {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	return false
}

// ruleLine reports whether a line is a section rule, and returns its title.
func ruleLine(line string) (string, bool) {
	plain := strings.TrimSpace(stripANSI(line))
	if !strings.HasPrefix(plain, "─") {
		return "", false
	}
	trimmed := strings.Trim(plain, "─ ")
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

func TestNumberedActionsAscendInFrameReadingOrder(t *testing.T) {
	cells := frameActionCells(renderFrame(sampleState(), 120, false))
	if len(cells) != 16 {
		t.Fatalf("the frame drew %d numbered actions, want the menu's 16", len(cells))
	}
	for index, cell := range cells {
		if want := strconv.Itoa(index + 1); cell.key != want {
			t.Fatalf("reading order %d is key %q (label %q), want %q: the numbering and the reading order disagree",
				index, cell.key, cell.label, want)
		}
	}
	// A grid read downwards must ascend as well, or the operator counting down a
	// column sees one sequence and the terminal shows another.
	for _, block := range gridBlocks(cells) {
		for _, column := range blockColumns(block) {
			for index := 1; index < len(column); index++ {
				previous, _ := strconv.Atoi(column[index-1].key)
				current, _ := strconv.Atoi(column[index].key)
				if current <= previous {
					t.Fatalf("column %d of a grid does not ascend: %s then %s", column[0].col, column[index-1].key, column[index].key)
				}
			}
		}
	}
}

func TestNumberedActionsAscendWithResolutionsAndDispatchFromTheSameList(t *testing.T) {
	for _, count := range []int{1, 2, 3} {
		external := make([]runtime.ExternalComponent, count)
		for index := range external {
			external[index] = runtime.ExternalComponent{
				Name:    fmt.Sprintf("Component %d", index+1),
				Address: fmt.Sprintf("127.0.0.1:%d", 8000+index),
				Holder:  fmt.Sprintf("process (pid %d)", 100+index),
			}
		}
		items := actionItems(viewState{actions: append(menu(), resolutionItems(external)...)})
		state := viewState{actions: items}
		cells := frameActionCells(renderFrame(state, 120, false))
		if len(cells) != 16+count*2 {
			t.Fatalf("%d resolutions: frame drew %d numbered actions, want %d", count, len(cells), 16+count*2)
		}
		for index, cell := range cells {
			if want := strconv.Itoa(index + 1); cell.key != want {
				t.Fatalf("%d resolutions: reading order %d is key %q, want %q", count, index, cell.key, want)
			}
			_, found := lookupItem(items, cell.key)
			if !found {
				t.Fatalf("%d resolutions: advertised key %q does not resolve to a drawn item %q", count, cell.key, cell.label)
			}
		}
		if !strings.Contains(cells[len(cells)-1].label, "Exit") {
			t.Fatalf("%d resolutions: last numbered action is %q, want Exit", count, cells[len(cells)-1].label)
		}
	}
}

// gridBlocks groups cells into the contiguous grids the frame drew: a run of
// consecutive rows holding the same number of cells.
func gridBlocks(cells []actionCell) [][]actionCell {
	blocks := make([][]actionCell, 0, 4)
	current := make([]actionCell, 0, 16)
	rows := 0
	for _, cell := range cells {
		if len(current) > 0 && cell.row != current[len(current)-1].row {
			rows++
			if rows > 1 && len(current)/rows != countInRow(current, cell.row) {
				blocks = append(blocks, current)
				current = make([]actionCell, 0, 16)
				rows = 0
			}
		}
		current = append(current, cell)
	}
	if len(current) > 0 {
		blocks = append(blocks, current)
	}
	return blocks
}

func countInRow(cells []actionCell, row int) int {
	count := 0
	for _, cell := range cells {
		if cell.row == row {
			count++
		}
	}
	return count
}

// blockColumns returns one slice per column of a grid block, top to bottom.
func blockColumns(block []actionCell) [][]actionCell {
	width := countInRow(block, block[0].row)
	columns := make([][]actionCell, width)
	for _, cell := range block {
		if cell.col < width {
			columns[cell.col] = append(columns[cell.col], cell)
		}
	}
	return columns
}

func TestExitIsTheLastNumberedAction(t *testing.T) {
	cells := frameActionCells(renderFrame(sampleState(), 120, false))
	if len(cells) == 0 {
		t.Fatal("the frame drew no numbered actions")
	}
	last := cells[len(cells)-1]
	if !strings.Contains(last.label, "Exit") {
		t.Fatalf("the last action drawn is %q (%s), want Exit last: an operator who reads to the end of the list must not find more work after the way out",
			last.label, last.key)
	}
}

func TestConventionKeysAreExplainedInTheFrame(t *testing.T) {
	text := frameText(sampleState(), 120, false)
	for _, explanation := range []struct{ key, meaning string }{
		{key: "0", meaning: "overview"},
		{key: "r", meaning: "refresh"},
		{key: "q", meaning: "exit"},
	} {
		pattern := regexp.MustCompile(`(?i)\b` + explanation.key + `\b[^A-Za-z0-9]{1,4}` + explanation.meaning)
		if !pattern.MatchString(text) {
			t.Fatalf("the frame never explains that %q means %s: keys that are conventions have to be named where they are offered",
				explanation.key, explanation.meaning)
		}
	}
	for _, cell := range frameActionCells(renderFrame(sampleState(), 120, false)) {
		if cell.key == "0" {
			t.Fatalf("key 0 (%s) is numbered into the action sequence, so the sequence is not a sequence", cell.label)
		}
	}
}

func TestSectionsAreSeparatedFromTheContentBeforeThem(t *testing.T) {
	lines := renderFrame(sampleState(), 120, false)
	actions, troubleshooting := -1, -1
	for index, line := range lines {
		if title, ok := ruleLine(line); ok {
			switch {
			case strings.HasPrefix(title, "Actions"):
				actions = index
			case strings.HasPrefix(title, "Troubleshooting"):
				troubleshooting = index
			}
		}
	}
	if actions < 0 {
		t.Fatal("the frame has no Actions section rule")
	}
	if strings.TrimSpace(stripANSI(lines[actions-1])) != "" {
		t.Fatalf("Actions starts immediately after %q: recorded events run into the action list",
			strings.TrimSpace(stripANSI(lines[actions-1])))
	}
	if troubleshooting < 0 {
		t.Fatal("the troubleshooting block has no rule of its own, so it reads as a continuation of the action list")
	}
}

func TestNoEventLineIsSilentlyClipped(t *testing.T) {
	// The owner's frame, which is what a clipped line looks like: a token cut
	// mid-word with the remainder unrecoverable from the frame.
	const event = "Finished `dev` profile [unoptimized + debuginfo] target(s) in 3.21s and wrote 12 artifacts"
	state := sampleState()
	state.events = []string{event}
	const width = 100
	lines := renderFrame(state, width, false)
	for index, line := range lines {
		if got := renderedWidth(line); got > width {
			t.Fatalf("line %d is %d columns wide, past the %d-column frame: %q", index, got, width, line)
		}
	}
	rendered := make([]string, 0, 4)
	continues := false
	for _, line := range lines {
		plain := stripANSI(line)
		switch {
		case strings.Contains(plain, "• "):
			rendered = append(rendered, plain[strings.Index(plain, "• ")+len("• "):])
		case strings.Contains(plain, continuationMark+" "):
			continues = true
			rendered = append(rendered, plain[strings.Index(plain, continuationMark+" ")+len(continuationMark)+1:])
		}
	}
	if !continues {
		t.Fatalf("the event was not wrapped onto a continuation line: %q", strings.Join(rendered, "|"))
	}
	if got := strings.Join(rendered, ""); !strings.Contains(got, event) {
		t.Fatalf("the frame lost part of the event:\n  frame: %q\n  event: %q", got, event)
	}
}

func TestReadOnlyActionsCarryADifferentMarkThanProcessActions(t *testing.T) {
	cells := frameActionCells(renderFrame(sampleState(), 120, false))
	var readOnly, process string
	for _, cell := range cells {
		switch {
		case strings.Contains(cell.label, "Status View"), strings.Contains(cell.label, "Log View"):
			readOnly = cell.mark
		case strings.Contains(cell.label, "Start All"), strings.Contains(cell.label, "Stop All"):
			process = cell.mark
		}
	}
	if readOnly == "" || process == "" {
		t.Fatalf("the frame did not draw both kinds of action: read-only %q, process %q", readOnly, process)
	}
	if readOnly == process {
		t.Fatalf("read-only and process-affecting actions share the mark %q: an operator cannot tell which keys change the machine", readOnly)
	}
	legend := frameText(sampleState(), 120, false)
	if !strings.Contains(legend, readOnly) || !strings.Contains(legend, process) {
		t.Fatalf("the frame uses the marks %q and %q without explaining them", readOnly, process)
	}
}

func TestStatusesAreDistinguishableWithoutColour(t *testing.T) {
	text := frameText(sampleState(), 120, false)
	seen := map[rune]string{}
	for _, line := range strings.Split(text, "\n") {
		plain := strings.TrimSpace(line)
		for _, component := range []string{"Control Plane", "Device Service", "Desktop Application"} {
			if strings.HasPrefix(plain, "●") || strings.HasPrefix(plain, "◐") || strings.HasPrefix(plain, "×") || strings.HasPrefix(plain, "○") || strings.HasPrefix(plain, "◌") || strings.HasPrefix(plain, "◑") {
				if strings.Contains(plain, component) {
					seen[[]rune(plain)[0]] = component
				}
			}
		}
	}
	if len(seen) != 3 {
		t.Fatalf("with colour off the frame shows %d distinct status marks for 3 components: %v", len(seen), seen)
	}
}

func TestClipLineMarksWhatItHadToDrop(t *testing.T) {
	const line = "Finished `dev` profile [unoptimized + debuginfo]"
	for _, width := range []int{8, 16, 40} {
		got := clipLine(line, width)
		if renderedWidth(got) > width {
			t.Fatalf("clipLine(width=%d) is %d columns: %q", width, renderedWidth(got), got)
		}
		if width < renderedWidth(line) && !strings.HasSuffix(got, ellipsisMark) {
			t.Fatalf("clipLine(width=%d) dropped content without saying so: %q", width, got)
		}
	}
	if got := clipLine("short", 40); got != "short" {
		t.Fatalf("clipLine changed a line that fits: %q", got)
	}
}
