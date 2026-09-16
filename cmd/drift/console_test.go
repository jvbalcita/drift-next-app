package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/runtime"
)

type fakeTerminal struct {
	isTTY    bool
	width    int
	height   int
	rawErr   error
	rawCalls int
	restored int
}

func (f *fakeTerminal) IsTerminal() bool { return f.isTTY }
func (f *fakeTerminal) Size() (int, int) { return f.width, f.height }

func (f *fakeTerminal) EnableRaw() (func(), error) {
	if f.rawErr != nil {
		return nil, f.rawErr
	}
	f.rawCalls++
	return func() { f.restored++ }, nil
}

type fakeOps struct {
	mu        sync.Mutex
	calls     []string
	statuses  []runtime.ComponentStatus
	logs      []string
	sink      func(string)
	release   chan struct{}
	err       error
	refreshes int
	// external is what the console is told is served by a process this session
	// did not start.
	external []runtime.ExternalComponent
}

func newFakeOps() *fakeOps {
	return &fakeOps{statuses: []runtime.ComponentStatus{{Name: "Control Plane", State: "ready", Detail: "Ready"}}}
}

func (f *fakeOps) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeOps) action(name string) func(context.Context) error {
	return func(context.Context) error {
		f.record(name)
		f.mu.Lock()
		release := f.release
		f.mu.Unlock()
		if release != nil {
			<-release
		}
		return f.err
	}
}

func (f *fakeOps) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeOps) called(name string) bool {
	for _, call := range f.callList() {
		if call == name {
			return true
		}
	}
	return false
}

func (f *fakeOps) refreshCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshes
}

func (f *fakeOps) eventSink() func(string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sink
}

func (f *fakeOps) asOps() ops {
	return ops{
		status: func() []runtime.ComponentStatus {
			f.mu.Lock()
			defer f.mu.Unlock()
			return append([]runtime.ComponentStatus(nil), f.statuses...)
		},
		logs: func() []string {
			f.mu.Lock()
			defer f.mu.Unlock()
			return append([]string(nil), f.logs...)
		},
		refresh: func(context.Context) error {
			f.mu.Lock()
			f.refreshes++
			f.mu.Unlock()
			f.record("refresh")
			return nil
		},
		setEventSink: func(sink func(string)) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.sink = sink
		},
		setup:           f.action("setup"),
		startAll:        f.action("startAll"),
		stopAll:         f.action("stopAll"),
		runChecks:       f.action("runChecks"),
		buildAll:        f.action("buildAll"),
		realDeviceTests: f.action("realDeviceTests"),
		startComponent: func(_ context.Context, name string) error {
			f.record("startComponent:" + name)
			return f.err
		},
		stopComponent: func(name string) error {
			f.record("stopComponent:" + name)
			return f.err
		},
		externalComponents: func() []runtime.ExternalComponent {
			f.mu.Lock()
			defer f.mu.Unlock()
			return append([]runtime.ExternalComponent(nil), f.external...)
		},
		adopt: func(name string) error {
			f.record("adopt:" + name)
			return f.err
		},
		terminate: func(_ context.Context, name string) error {
			f.record("terminate:" + name)
			return f.err
		},
	}
}

func newTestConsole(t *testing.T, fake *fakeOps, out io.Writer, term terminal, input string, color bool) *console {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	console := newConsole(ctx, fake.asOps(), out, term, strings.NewReader(input), color)
	console.tickInterval = 5 * time.Millisecond
	console.refreshInterval = time.Hour
	return console
}

// failingWriter fails every write after the given number of successes.
type failingWriter struct {
	allowed int
	writes  int
}

func (w *failingWriter) Write(payload []byte) (int, error) {
	w.writes++
	if w.writes > w.allowed {
		return 0, errors.New("terminal write failed")
	}
	return len(payload), nil
}

func containsSpinnerFrame(text string) bool {
	for _, frame := range spinnerFrames {
		if strings.Contains(text, frame) {
			return true
		}
	}
	return false
}

func TestEventSinkBuffersEventsForTheFrameInsteadOfWritingToStdout(t *testing.T) {
	out := &recordingWriter{}
	fake := newFakeOps()
	console := newTestConsole(t, fake, out, &fakeTerminal{isTTY: false, width: 100}, "", false)

	sink := fake.eventSink()
	if sink == nil {
		t.Fatal("the console did not register an event sink")
	}
	sink("ADB discovered and validated")
	sink("All requested components are ready")

	if out.writeCount() != 0 {
		t.Fatalf("the event sink wrote straight to the terminal: %q", out.written())
	}

	console.state.events = console.events.snapshot()
	text := frameText(console.state, 100, false)
	rule := strings.Index(text, "Recent events")
	first := strings.Index(text, "ADB discovered and validated")
	second := strings.Index(text, "All requested components are ready")
	if first < 0 || second < 0 {
		t.Fatalf("buffered events are missing from the frame: %q", text)
	}
	if first < rule || second < rule {
		t.Fatal("an event rendered outside the events region")
	}
	if first > second {
		t.Fatalf("events rendered out of order: first=%d second=%d", first, second)
	}
	if out.writeCount() != 0 {
		t.Fatal("rendering the frame wrote to the terminal outside paint")
	}
}

func TestConsoleRendersInPlaceAndRestoresTheTerminalOnceOnExit(t *testing.T) {
	out := &recordingWriter{}
	fake := newFakeOps()
	term := &fakeTerminal{isTTY: true, width: 90}
	console := newTestConsole(t, fake, out, term, "q\r", true)

	if err := console.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	text := out.written()
	if !strings.Contains(text, "Drift Next · Local Runtime") {
		t.Fatalf("no frame was rendered: %q", text)
	}
	if strings.Contains(text, "Press Enter") {
		t.Fatal("the console still blocks for Enter after an action")
	}
	if term.rawCalls != 1 {
		t.Fatalf("raw input mode was enabled %d times, want once", term.rawCalls)
	}
	if term.restored != 1 {
		t.Fatalf("terminal mode was restored %d times, want once", term.restored)
	}
	if restores := strings.Count(text, eraseBelow); restores != 1 {
		t.Fatalf("the frame restore ran %d times, want exactly once", restores)
	}
	if !fake.called("stopAll") {
		t.Fatal("exiting did not stop owned processes")
	}
}

func TestConsoleRestoresTheTerminalWhenRenderingFails(t *testing.T) {
	out := &failingWriter{allowed: 0}
	fake := newFakeOps()
	term := &fakeTerminal{isTTY: true, width: 90}
	console := newTestConsole(t, fake, out, term, "q\r", true)

	err := console.run()
	if err == nil {
		t.Fatal("a failing terminal write did not surface as an error")
	}
	if term.restored != 1 {
		t.Fatalf("terminal mode was restored %d times after an error, want once", term.restored)
	}
	if !fake.called("stopAll") {
		t.Fatal("the error path did not stop owned processes")
	}
}

func TestConsoleRunsTheActionBehindEveryMenuKey(t *testing.T) {
	cases := []struct{ key, want string }{
		{key: "1", want: "setup"},
		{key: "2", want: "startAll"},
		{key: "5", want: "runChecks"},
		{key: "6", want: "buildAll"},
		{key: "7", want: "realDeviceTests"},
		{key: "11", want: "startComponent:Control Plane"},
		{key: "12", want: "startComponent:Device Service"},
		{key: "13", want: "startComponent:Desktop Application"},
		{key: "14", want: "stopComponent:Control Plane"},
		{key: "15", want: "stopComponent:Device Service"},
		{key: "16", want: "stopComponent:Desktop Application"},
	}
	for _, testCase := range cases {
		fake := newFakeOps()
		console := newTestConsole(t, fake, &recordingWriter{}, &fakeTerminal{isTTY: false, width: 80}, "", false)
		console.state.input = testCase.key
		if quit := console.submit(); quit {
			t.Fatalf("key %q quit the console", testCase.key)
		}
		select {
		case result := <-console.actionDone:
			if result.err != nil {
				t.Fatalf("key %q failed: %v", testCase.key, result.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("key %q never ran its action", testCase.key)
		}
		if !fake.called(testCase.want) {
			t.Fatalf("key %q called %v, want %s", testCase.key, fake.callList(), testCase.want)
		}
	}
}

func TestConsoleRestartKeyStopsThenStarts(t *testing.T) {
	fake := newFakeOps()
	console := newTestConsole(t, fake, &recordingWriter{}, &fakeTerminal{isTTY: false, width: 80}, "", false)
	console.state.input = "4"
	if quit := console.submit(); quit {
		t.Fatal("the restart key quit the console")
	}
	select {
	case <-console.actionDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the restart action never ran")
	}
	if calls := fake.callList(); len(calls) != 2 || calls[0] != "stopAll" || calls[1] != "startAll" {
		t.Fatalf("restart called %v, want stopAll then startAll", calls)
	}
}

func TestConsoleShowsProgressWhileAnActionRuns(t *testing.T) {
	fake := newFakeOps()
	release := make(chan struct{})
	fake.release = release
	out := &recordingWriter{}
	term := &fakeTerminal{isTTY: true, width: 100}
	console := newTestConsole(t, fake, out, term, "5\rq\r", true)
	console.tickInterval = 2 * time.Millisecond
	go func() {
		time.Sleep(80 * time.Millisecond)
		close(release)
	}()

	if err := console.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	text := out.written()
	if !strings.Contains(text, "Run All Checks") {
		t.Fatalf("the running action was never labelled in the frame: %q", text)
	}
	if !containsSpinnerFrame(text) {
		t.Fatalf("no progress indicator appeared while the action ran: %q", text)
	}
	if !strings.Contains(text, "completed in") {
		t.Fatalf("the action outcome was never reported: %q", text)
	}
	if repaints := strings.Count(text, "Drift Next · Local Runtime"); repaints < 3 {
		t.Fatalf("the frame was painted %d times, want live repaints during the action", repaints)
	}
	if strings.Contains(text, "Press Enter") {
		t.Fatal("the console still prompted for Enter to return to the dashboard")
	}
	if console.state.mode != viewDashboard {
		t.Fatalf("the console did not return to the dashboard (mode %d)", console.state.mode)
	}
}

func TestConsoleTogglesViewsWithoutLeavingTheFrame(t *testing.T) {
	fake := newFakeOps()
	console := newTestConsole(t, fake, &recordingWriter{}, &fakeTerminal{isTTY: false, width: 80}, "", false)

	console.state.input = "8"
	console.submit()
	if console.state.mode != viewStatus {
		t.Fatalf("key 8 selected mode %d, want the status view", console.state.mode)
	}
	console.state.input = "8"
	console.submit()
	if console.state.mode != viewDashboard {
		t.Fatalf("key 8 again left mode %d, want the dashboard", console.state.mode)
	}
	console.state.input = "9"
	console.submit()
	if console.state.mode != viewLogs {
		t.Fatalf("key 9 selected mode %d, want the log view", console.state.mode)
	}
	console.state.input = "0"
	console.submit()
	if console.state.mode != viewDashboard {
		t.Fatalf("key 0 left mode %d, want the dashboard", console.state.mode)
	}
	if len(fake.callList()) != 0 {
		t.Fatalf("view keys ran actions: %v", fake.callList())
	}
}

func TestConsoleRejectsAnUnknownKey(t *testing.T) {
	fake := newFakeOps()
	console := newTestConsole(t, fake, &recordingWriter{}, &fakeTerminal{isTTY: false, width: 80}, "", false)
	console.state.input = "99"
	if quit := console.submit(); quit {
		t.Fatal("an unknown key quit the console")
	}
	if !strings.Contains(console.state.notice, "Unknown action") {
		t.Fatalf("unknown key notice = %q", console.state.notice)
	}
	if len(fake.callList()) != 0 {
		t.Fatalf("an unknown key ran something: %v", fake.callList())
	}
}

func TestConsoleRefreshesOnATimerAndOnAManualKey(t *testing.T) {
	fake := newFakeOps()
	console := newTestConsole(t, fake, &recordingWriter{}, &fakeTerminal{isTTY: false, width: 80}, "", false)

	console.lastRefresh = console.now()
	console.onTick()
	if fake.refreshCount() != 0 {
		t.Fatal("the dashboard refreshed before the refresh interval elapsed")
	}

	console.lastRefresh = console.now().Add(-time.Hour)
	fake.mu.Lock()
	fake.statuses = []runtime.ComponentStatus{{Name: "Control Plane", State: "ready", Detail: "Ready (already running)"}}
	fake.mu.Unlock()
	console.onTick()
	if fake.refreshCount() != 1 {
		t.Fatalf("a stale dashboard refreshed %d times, want 1", fake.refreshCount())
	}
	if got := console.state.components[0].Detail; got != "Ready (already running)" {
		t.Fatalf("status detail = %q, want the refreshed value", got)
	}

	console.state.input = ""
	console.submit()
	if fake.refreshCount() != 2 {
		t.Fatal("Enter alone did not refresh the dashboard")
	}
	console.state.input = "r"
	console.submit()
	if fake.refreshCount() != 3 {
		t.Fatal("the manual refresh key did not refresh the dashboard")
	}
}

func TestConsoleEditsTypedInput(t *testing.T) {
	fake := newFakeOps()
	console := newTestConsole(t, fake, &recordingWriter{}, &fakeTerminal{isTTY: false, width: 80}, "", false)

	if quit := console.handleKey('1'); quit {
		t.Fatal("typing a digit quit the console")
	}
	console.handleKey('6')
	if console.state.input != "16" {
		t.Fatalf("typed input = %q, want %q", console.state.input, "16")
	}
	console.handleKey(0x7f)
	if console.state.input != "1" {
		t.Fatalf("backspace left %q, want %q", console.state.input, "1")
	}
	console.handleKey(0x1b)
	if console.state.input != "" {
		t.Fatalf("escape left %q, want empty input", console.state.input)
	}
	if quit := console.handleKey(0x03); !quit {
		t.Fatal("Ctrl-C did not quit the console")
	}
	if len(fake.callList()) != 0 {
		t.Fatalf("editing input ran actions: %v", fake.callList())
	}
}

func TestConsoleDegradesToPlainTextWithoutATerminal(t *testing.T) {
	fake := newFakeOps()
	out := &recordingWriter{}
	term := &fakeTerminal{isTTY: false, width: 80}
	console := newTestConsole(t, fake, out, term, "8\r9\r\rq\r", false)

	if err := console.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	text := out.written()
	if strings.ContainsRune(text, 0x1b) {
		t.Fatalf("non-interactive output contained escape sequences: %q", text)
	}
	if term.rawCalls != 0 {
		t.Fatal("raw input mode was enabled without a terminal")
	}
	if !strings.Contains(text, "Drift Next · Local Runtime") {
		t.Fatalf("no frame was rendered: %q", text)
	}
	if !strings.Contains(text, "Component status") {
		t.Fatalf("the status view was never rendered: %q", text)
	}
}

func TestConsoleWritesPlainTextWhenEscapesAreDisabledOnATerminal(t *testing.T) {
	// 'color' being false means "no escape codes at all", which is what a
	// NO_COLOR terminal is promised even though the stream is interactive.
	fake := newFakeOps()
	out := &recordingWriter{}
	term := &fakeTerminal{isTTY: true, width: 80}
	console := newTestConsole(t, fake, out, term, "8\rq\r", false)

	if err := console.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	text := out.written()
	if strings.ContainsRune(text, 0x1b) {
		t.Fatalf("plain mode emitted escape sequences on a terminal: %q", text)
	}
	if !strings.Contains(text, "Drift Next · Local Runtime") {
		t.Fatalf("no frame was rendered: %q", text)
	}
	if term.rawCalls != 1 {
		t.Fatalf("raw input mode was enabled %d times, want once", term.rawCalls)
	}
}

func TestConsoleFallsBackToCanonicalInputWhenRawModeFails(t *testing.T) {
	fake := newFakeOps()
	out := &recordingWriter{}
	term := &fakeTerminal{isTTY: true, width: 80, rawErr: errors.New("not supported")}
	console := newTestConsole(t, fake, out, term, "q\r", false)

	if err := console.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if term.restored != 0 {
		t.Fatal("a raw-mode failure still tried to restore terminal mode")
	}
	if !strings.Contains(out.written(), "Drift Next · Local Runtime") {
		t.Fatalf("the console did not keep running without raw mode: %q", out.written())
	}
}
