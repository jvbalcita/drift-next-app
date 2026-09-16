package main

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"drift.local/drift-next/internal/runtime"
)

// The console is deliberately dependency-free: frames are plain string
// composition plus the small amount of raw ANSI written by painter.go.
//
// renderFrame is a pure function of (state, width, colour mode) so the frame can
// be asserted in tests without a terminal.

const (
	sgrReset  = "\033[0m"
	sgrGreen  = "\033[32m"
	sgrRed    = "\033[31m"
	sgrYellow = "\033[33m"
	sgrCyan   = "\033[36m"
	sgrDim    = "\033[2m"
)

const (
	// continuationMark prefixes the second and later rows of one logical line
	// that had to wrap inside a bounded region, so a wrapped line is visibly one
	// line rather than several events.
	continuationMark = "↳"
	// ellipsisMark ends a line that had to be cut to fit. Nothing is dropped
	// without it: an operator reading a shortened line can see that it is short.
	ellipsisMark = "…"
)

const (
	// readOnlyMark and processMark classify a key in the action grid. An operator
	// has to see which keys change what is running, and colour is not always
	// available (NO_COLOR, piped output, a captured frame), so the classification
	// is a glyph — and both glyphs are explained in the frame's key legend, never
	// used silently.
	// readOnlyMark marks a key that reads state and changes nothing.
	readOnlyMark = "◦"
	// processMark marks a key that starts, stops, builds or otherwise changes
	// what is running on the machine.
	processMark = "▸"
	// markColumn is the gap between the classification glyph and the key. The
	// keys line up in a column under it, which is also what the frame reader uses
	// to tell a classification glyph from part of a label.
	markColumn = "  "
	// bulletMark prefixes an event line, and is as wide as continuationMark so the
	// wrapped text of one event stays aligned under itself.
	bulletMark = "• "
	// regionPrefix is the left inset every bounded region's rows carry, and
	// regionIndent is its width. One source of truth, so the wrap budget and the
	// rendering cannot disagree about how much room the inset takes.
	regionPrefix = "  "
	regionIndent = len(regionPrefix)
)

const (
	// eventWrapWidth is the reading measure an event line wraps at inside its
	// bounded region, even when the frame is wider. Bounding the region must not
	// lose text: an event longer than this continues on a marked continuation
	// line rather than being cut. A line the full width of a wide terminal is
	// also harder to read than two short ones.
	eventWrapWidth = 76
	// eventOverflowNotice stands in for the rows an event needed but the region
	// could not show. It names how many are withheld and where the whole text is,
	// so a bounded region is never mistaken for the whole of what arrived.
	eventOverflowNotice = "%d more rows of this event - key 9 opens the log view"
)

const (
	// fallbackFrameWidth is used when the terminal size cannot be read (piped
	// output, unknown window size, or a platform without a size query).
	fallbackFrameWidth = 100
	// minFrameWidth is the narrowest frame worth drawing.
	minFrameWidth = 20
	// maxFrameWidth keeps the frame readable on very wide terminals.
	maxFrameWidth = 120

	// eventRegionRows and logRegionRows bound the two scrolling regions so a
	// chatty runtime can never push the frame off the screen.
	eventRegionRows = 6
	logRegionRows   = 20

	menuColumnWidth = 26
	menuMaxColumns  = 3
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// palette styles text, or leaves it alone when colour is disabled. A frame
// rendered with a disabled palette contains no escape sequences at all.
type palette struct{ enabled bool }

func (p palette) style(code, text string) string {
	if !p.enabled || code == "" || text == "" {
		return text
	}
	return code + text + sgrReset
}

// viewMode selects which panel replaces the event region.
type viewMode int

const (
	viewDashboard viewMode = iota
	viewStatus
	viewLogs
)

// noticeLevel classifies the outcome line of the most recent action.
type noticeLevel int

const (
	noticeNone noticeLevel = iota
	noticeOK
	noticeFail
)

// viewState is everything the frame renders. It is mutated only by the console
// loop goroutine, so rendering never observes a half-applied update.
type viewState struct {
	components []runtime.ComponentStatus
	events     []string
	logs       []string
	mode       viewMode
	// actions is the item list this frame renders and dispatches. The console
	// supplies it, because a resolution exists only while the condition it
	// resolves exists. renderFrame falls back to the static menu when it is
	// empty, so a frame never advertises a key it cannot dispatch.
	actions     []menuItem
	input       string
	busy        bool
	busyLabel   string
	busyElapsed time.Duration
	spinner     int
	notice      string
	noticeLevel noticeLevel
	refreshed   time.Time
}

// clampWidth maps a terminal width onto a drawable frame width: a sane minimum,
// a sane maximum, and a fallback when the size is unknown.
func clampWidth(width int) int {
	switch {
	case width <= 0:
		return fallbackFrameWidth
	case width < minFrameWidth:
		return minFrameWidth
	case width > maxFrameWidth:
		return maxFrameWidth
	default:
		return width
	}
}

// visibleWidth counts the columns a line occupies, ignoring ANSI sequences.
func visibleWidth(text string) int {
	width := 0
	for index := 0; index < len(text); {
		if text[index] == 0x1b {
			index = skipEscape(text, index)
			continue
		}
		_, size := utf8.DecodeRuneInString(text[index:])
		index += size
		width++
	}
	return width
}

func skipEscape(text string, index int) int {
	next := index + 1
	if next < len(text) && text[next] == '[' {
		next++
		for next < len(text) && (text[next] == ';' || (text[next] >= '0' && text[next] <= '9')) {
			next++
		}
		if next < len(text) {
			next++
		}
		return next
	}
	if next < len(text) {
		return next + 1
	}
	return next
}

// clipLine truncates a line to width printable columns, keeping escape
// sequences intact and re-closing a style it had to cut. The last column is
// reserved for the ellipsis whenever anything is dropped: a line that was cut
// says so, so an operator never reads a shortened line as a complete one.
func clipLine(text string, width int) string {
	if width <= 0 || visibleWidth(text) <= width {
		return text
	}
	limit := width - visibleWidth(ellipsisMark)
	if limit < 0 {
		limit = 0
	}
	var builder strings.Builder
	columns, truncated, styled := 0, false, false
	for index := 0; index < len(text); {
		if text[index] == 0x1b {
			next := skipEscape(text, index)
			builder.WriteString(text[index:next])
			index, styled = next, true
			continue
		}
		rune_, size := utf8.DecodeRuneInString(text[index:])
		if columns >= limit {
			truncated = true
			break
		}
		builder.WriteRune(rune_)
		columns++
		index += size
	}
	if truncated {
		builder.WriteString(ellipsisMark)
		// Only re-close a style if this line actually carried escape sequences:
		// plain frames must stay escape-free.
		if styled {
			builder.WriteString(sgrReset)
		}
	}
	return builder.String()
}

// tailWindow returns at most the most recent limit lines, in their original
// order, without mutating the caller's slice.
func tailWindow(lines []string, limit int) []string {
	if limit <= 0 || len(lines) == 0 {
		return nil
	}
	if len(lines) <= limit {
		return append([]string(nil), lines...)
	}
	return append([]string(nil), lines[len(lines)-limit:]...)
}

// eventRows renders one event as the rows it occupies inside the bounded event
// region: the bullet row, then one marked continuation row per wrap. The event
// text is carried whole — the region is bounded so the frame height stays
// stable, never so that the rest of a line can be dropped — and the wrap measure
// keeps a long line readable on a wide terminal instead of running the full
// width of it.
func eventRows(event string, width int, p palette) []string {
	budget := width - regionIndent - utf8.RuneCountInString(bulletMark)
	switch {
	case budget < 1:
		budget = 1
	case budget > eventWrapWidth:
		budget = eventWrapWidth
	}
	chunks := wrapRunes(event, budget)
	rows := make([]string, 0, len(chunks))
	for index, chunk := range chunks {
		if index == 0 {
			rows = append(rows, p.style(sgrDim, bulletMark)+chunk)
			continue
		}
		rows = append(rows, p.style(sgrDim, continuationMark+" ")+chunk)
	}
	return rows
}

// wrapRunes splits text into chunks of at most budget columns, preferring the
// last space at or before the budget so a wrap lands between words where one is
// available. Every character is kept, including the space a wrap breaks after,
// so the chunks concatenate back to the original text exactly: wrapping may
// change where a line ends, never what it says.
func wrapRunes(text string, budget int) []string {
	runes := []rune(text)
	if budget < 1 || len(runes) <= budget {
		return []string{text}
	}
	chunks := make([]string, 0, len(runes)/budget+1)
	for len(runes) > 0 {
		if len(runes) <= budget {
			chunks = append(chunks, string(runes))
			break
		}
		cut := budget
		for index := budget; index > 0; index-- {
			if runes[index-1] == ' ' {
				cut = index
				break
			}
		}
		chunks = append(chunks, string(runes[:cut]))
		runes = runes[cut:]
	}
	return chunks
}

// renderFrame renders the whole frame, one element per line, with the input
// prompt as the last line so the terminal cursor rests where typing appears.
func renderFrame(state viewState, width int, color bool) []string {
	p := palette{enabled: color}
	frameWidth := clampWidth(width)
	lines := []string{rule("Drift Next · Local Runtime", refreshedLabel(state), frameWidth, p)}
	for _, component := range state.components {
		lines = append(lines, renderComponent(component, p))
	}
	lines = append(lines, renderActivity(state, p))
	switch state.mode {
	case viewStatus:
		lines = append(lines, rule("Component status", "", frameWidth, p))
		lines = append(lines, renderStatusPanel(state.components)...)
	case viewLogs:
		window := tailWindow(state.logs, logRegionRows)
		lines = append(lines, rule(fmt.Sprintf("Runtime log · last %d of %d", len(window), len(state.logs)), "", frameWidth, p))
		lines = append(lines, renderWindow(window, "No runtime log lines yet.", logRegionRows, p)...)
	default:
		window := tailWindow(state.events, eventRegionRows)
		bulleted := make([]string, 0, len(window))
		for _, event := range window {
			bulleted = append(bulleted, eventRows(event, frameWidth, p)...)
		}
		// The region is bounded, so an event longer than every row of it cannot
		// be shown whole. Say which rows were withheld and where the text is,
		// rather than dropping them without a trace: bounding a region must never
		// be the same thing as losing content quietly.
		if len(bulleted) > eventRegionRows {
			withheld := len(bulleted) - (eventRegionRows - 1)
			notice := p.style(sgrDim, continuationMark+" ") + fmt.Sprintf(eventOverflowNotice, withheld)
			bulleted = append(bulleted[:eventRegionRows-1], notice)
		}
		lines = append(lines, rule("Recent events", "", frameWidth, p))
		lines = append(lines, renderWindow(bulleted, "No events recorded yet.", eventRegionRows, p)...)
	}
	lines = append(lines, rule("Actions", "", frameWidth, p))
	lines = append(lines, renderMenu(actionItems(state), frameWidth, p)...)
	lines = append(lines, keyLegend(p))
	lines = append(lines, renderPrompt(state, p))
	for index, line := range lines {
		lines[index] = clipLine(line, frameWidth)
	}
	return lines
}

// actionItems is the item list a frame renders and dispatches: the console's
// list when it has one, and the static menu otherwise. Both the grid and the
// prompt range come from the same list, so a frame can never advertise a key it
// cannot dispatch.
func actionItems(state viewState) []menuItem {
	if len(state.actions) == 0 {
		return menu()
	}
	return state.actions
}

// rule renders a section separator that fills the frame width.
func rule(title, suffix string, width int, p palette) string {
	head := "─ " + title + " "
	tail := ""
	if suffix != "" {
		tail = " " + suffix + " "
	}
	padding := width - visibleWidth(head) - visibleWidth(tail)
	if padding < 1 {
		padding = 1
	}
	return p.style(sgrDim, head+strings.Repeat("─", padding)+tail)
}

func refreshedLabel(state viewState) string {
	if state.refreshed.IsZero() {
		return ""
	}
	return "refreshed " + state.refreshed.Format("15:04:05")
}

// renderComponent shows state as text as well as colour so status never depends
// on colour alone. "external" and "adopted" carry their own marks because they
// are neither failure nor health: a listener this session did not start is a
// condition the operator has to decide about, and an adopted one is a decision,
// not a verification.
func renderComponent(component runtime.ComponentStatus, p palette) string {
	state := string(component.State)
	icon, color := "○", sgrDim
	switch state {
	case "ready":
		icon, color = "●", sgrGreen
	case "starting":
		icon, color = "◐", sgrYellow
	case "failed":
		icon, color = "×", sgrRed
	case "external":
		icon, color = "◌", sgrYellow
	case "adopted":
		icon, color = "◑", sgrCyan
	}
	detail := strings.TrimSpace(component.Detail)
	if detail == "" {
		detail = "Not started"
	}
	return fmt.Sprintf("  %s %-22s %-9s %s", p.style(color, icon), component.Name, p.style(sgrDim, state), p.style(sgrDim, detail))
}

// renderActivity keeps the outcome and the in-flight progress on one stable
// line so a long action cannot push the frame around.
func renderActivity(state viewState, p palette) string {
	switch {
	case state.busy:
		spinner := spinnerFrames[absolute(state.spinner)%len(spinnerFrames)]
		return fmt.Sprintf("  %s %s… %s", p.style(sgrYellow, spinner), state.busyLabel, elapsedLabel(state.busyElapsed))
	case state.notice != "":
		marker, color := "•", sgrCyan
		switch state.noticeLevel {
		case noticeOK:
			marker, color = "✓", sgrGreen
		case noticeFail:
			marker, color = "✗", sgrRed
		}
		return fmt.Sprintf("  %s %s", p.style(color, marker), state.notice)
	default:
		return p.style(sgrDim, "  Idle · r refreshes now · q quits")
	}
}

func elapsedLabel(elapsed time.Duration) string {
	return elapsed.Round(100 * time.Millisecond).String()
}

func absolute(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func renderStatusPanel(components []runtime.ComponentStatus) []string {
	lines := make([]string, 0, len(components))
	for _, component := range components {
		detail := strings.TrimSpace(component.Detail)
		if detail == "" {
			detail = "Not started"
		}
		lines = append(lines, fmt.Sprintf("  %-22s %-9s %s", component.Name, string(component.State), detail))
	}
	return lines
}

// renderWindow renders a bounded region, padded to a fixed number of rows so
// arriving events never change the frame height. Content is rendered at its
// own width: a caller that has more text than the region is wide wraps it into
// more rows, so bounding the region never costs the operator the rest of a line.
func renderWindow(content []string, empty string, rows int, p palette) []string {
	lines := make([]string, 0, rows)
	if len(content) == 0 {
		lines = append(lines, p.style(sgrDim, regionPrefix+empty))
	} else {
		for _, line := range content {
			lines = append(lines, regionPrefix+line)
		}
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return lines
}

// actionMark is the glyph drawn before a key to say whether the action changes
// what is running. A view reads state and changes nothing; every other key on
// offer starts, stops, builds or otherwise acts on the machine. The legend in
// the frame explains both glyphs, so the classification is never implied.
func actionMark(item menuItem) string {
	if item.Kind == kindView {
		return readOnlyMark
	}
	return processMark
}

// keyLegend names the keys that are conventions rather than numbered actions,
// and the two marks the grid uses. A key that is offered anywhere in the frame
// is explained in the frame: an operator should not have to know a convention,
// or guess a glyph, to read the surface.
func keyLegend(p palette) string {
	return p.style(sgrDim, "  Keys  0 overview · r refresh · q exit · "+
		processMark+" changes processes · "+readOnlyMark+" read-only")
}

func renderMenu(items []menuItem, width int, p palette) []string {
	primary := make([]string, 0, len(items))
	troubleshooting := make([]string, 0, len(items))
	resolutions := make([]string, 0, len(items))
	exits := make([]string, 0, len(items))
	for _, item := range items {
		if item.Shortcut {
			continue
		}
		cell := " " + actionMark(item) + markColumn + p.style(sgrCyan, item.Key) + " " + item.Label
		switch item.Group {
		case groupTroubleshooting:
			troubleshooting = append(troubleshooting, cell)
		case groupResolution:
			resolutions = append(resolutions, cell)
		case groupExit:
			exits = append(exits, cell)
		default:
			primary = append(primary, cell)
		}
	}
	lines := gridRows(primary, width)
	if len(resolutions) > 0 {
		// One full-width line per resolution, not a grid cell: the holder has to
		// be readable before the choice is made, and a column would clip it away.
		lines = append(lines, p.style(sgrDim, "  Needs a decision - nothing is changed until one is chosen"))
		lines = append(lines, resolutions...)
	}
	if len(troubleshooting) > 0 {
		// A rule of its own, not a dim label: the troubleshooting block is a
		// section an operator arrives at, not a continuation of the action list.
		lines = append(lines, rule("Troubleshooting", "", width, p))
		lines = append(lines, gridRows(troubleshooting, width)...)
	}
	if len(exits) > 0 {
		// Last, after every other group: an operator who reads to the end of the
		// list must not find more work after the way out.
		lines = append(lines, gridRows(exits, width)...)
	}
	return lines
}

func gridRows(cells []string, width int) []string {
	if len(cells) == 0 {
		return nil
	}
	columns := width / menuColumnWidth
	switch {
	case columns < 1:
		columns = 1
	case columns > menuMaxColumns:
		columns = menuMaxColumns
	}
	columnWidth := width / columns
	rows := (len(cells) + columns - 1) / columns
	lines := make([]string, 0, rows)
	for row := 0; row < rows; row++ {
		var builder strings.Builder
		for column := 0; column < columns; column++ {
			index := row*columns + column
			if index >= len(cells) {
				break
			}
			builder.WriteString(cells[index])
			if column < columns-1 {
				if padding := columnWidth - visibleWidth(cells[index]); padding > 0 {
					builder.WriteString(strings.Repeat(" ", padding))
				}
			}
		}
		lines = append(lines, builder.String())
	}
	return lines
}

// renderPrompt is the last frame line: the terminal cursor ends up right after
// the typed input, so typing never lands outside the stable frame.
func renderPrompt(state viewState, p palette) string {
	return p.style(sgrCyan, promptLabel(actionItems(state))) + state.input
}
