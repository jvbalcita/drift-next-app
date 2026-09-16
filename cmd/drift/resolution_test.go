package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/runtime"
)

func externalDeviceService() []runtime.ExternalComponent {
	return []runtime.ExternalComponent{{Name: "Device Service", Address: "127.0.0.1:8081", Holder: "node (pid 4711)"}}
}

// keyFor returns the advertised key of the first item whose label starts with
// prefix. Tests press the key the frame actually advertises rather than one they
// assumed, so an advert and its dispatch cannot drift apart unnoticed.
func keyFor(t *testing.T, items []menuItem, prefix string) string {
	t.Helper()
	for _, item := range items {
		if strings.HasPrefix(item.Label, prefix) {
			return item.Key
		}
	}
	t.Fatalf("no advertised item starts with %q", prefix)
	return ""
}

// A control is offered only while the thing it acts on exists.
func TestNoResolutionIsOfferedWhenNoListenerIsExternal(t *testing.T) {
	if items := resolutionItems(nil); len(items) != 0 {
		t.Fatalf("a resolution is offered with nothing external to resolve: %v", items)
	}
	text := frameText(sampleState(), 100, false)
	if strings.Contains(text, "Needs a decision") {
		t.Errorf("the frame offers a decision although nothing is external:\n%s", text)
	}
}

// The operator must see who holds the address before either choice is made -
// they cannot weigh adopting against terminating a process they have not been
// shown - and must be told that doing nothing is safe.
func TestTheFrameShowsTheHolderAndBothChoicesBeforeEitherIsMade(t *testing.T) {
	state := viewState{
		components: []runtime.ComponentStatus{{Name: "Device Service", State: "external", Detail: "127.0.0.1:8081 is served by node (pid 4711), a process this session did not start"}},
		actions:    append(menu(), resolutionItems(externalDeviceService())...),
	}
	text := frameText(state, 100, false)
	for _, want := range []string{"127.0.0.1:8081", "4711", "Adopt Device Service", "Terminate Device Service", "Needs a decision"} {
		if !strings.Contains(text, want) {
			t.Errorf("the frame does not show %q before the choice is made:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "nothing is changed until one is chosen") {
		t.Error("the frame does not say that making no choice changes nothing")
	}
	// The prompt advertises the key range rather than each key, so the claim to
	// check is that the range covers every resolution it is offering.
	low, high := keyRange(state.actions)
	for _, prefix := range []string{"Adopt Device Service", "Terminate Device Service"} {
		key, err := strconv.Atoi(keyFor(t, state.actions, prefix))
		if err != nil {
			t.Fatalf("the advertised key for %q is not a number: %v", prefix, err)
		}
		if key < low || key > high {
			t.Errorf("the prompt advertises keys %d-%d, which does not cover the advertised resolution key %d for %q", low, high, key, prefix)
		}
	}
}

// An advertised key must dispatch exactly what it advertises, and a resolution
// must never take a key that already means something else.
func TestResolutionKeysAreDistinctAndDispatchable(t *testing.T) {
	external := append(externalDeviceService(), runtime.ExternalComponent{Name: "Control Plane", Address: "127.0.0.1:8080", Holder: "go (pid 99)"})
	items := append(menu(), resolutionItems(external)...)

	seen := map[string]bool{}
	for _, item := range items {
		if seen[item.Key] {
			t.Fatalf("key %q is advertised twice", item.Key)
		}
		seen[item.Key] = true
	}
	for _, want := range []string{"Adopt Device Service", "Terminate Device Service", "Adopt Control Plane", "Terminate Control Plane"} {
		key := keyFor(t, items, want)
		resolved, found := lookupItem(items, key)
		if !found {
			t.Fatalf("the advertised key %q for %q does not resolve", key, want)
		}
		if resolved.Group != groupResolution {
			t.Fatalf("key %q resolves to group %v, want a resolution", key, resolved.Group)
		}
	}
	if nextResolutionKey() <= 16 {
		t.Errorf("resolution keys start at %d and would collide with the menu's own keys", nextResolutionKey())
	}
}

// Pressing the advertised key runs that decision and nothing else.
func TestConsoleRunsTheResolutionBehindTheKeyItAdvertised(t *testing.T) {
	for _, testCase := range []struct{ prefix, want string }{
		{prefix: "Adopt Device Service", want: "adopt:Device Service"},
		{prefix: "Terminate Device Service", want: "terminate:Device Service"},
	} {
		fake := newFakeOps()
		fake.external = externalDeviceService()
		console := newTestConsole(t, fake, &recordingWriter{}, &fakeTerminal{isTTY: false, width: 100}, "", false)
		console.refresh()

		key := keyFor(t, console.state.actions, testCase.prefix)
		console.state.input = key
		if quit := console.submit(); quit {
			t.Fatalf("key %q quit the console", key)
		}
		select {
		case result := <-console.actionDone:
			if result.err != nil {
				t.Fatalf("the resolution behind key %q failed: %v", key, result.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the resolution behind key %q never ran", key)
		}
		if !fake.called(testCase.want) {
			t.Fatalf("key %q called %v, want %s", key, fake.callList(), testCase.want)
		}
	}
}

// Doing nothing is a legitimate answer: no decision runs, the offer stands, and
// nothing is recorded as a failure. There is no countdown and nothing to cancel.
func TestMakingNoChoiceLeavesTheRuntimeUntouched(t *testing.T) {
	fake := newFakeOps()
	fake.external = externalDeviceService()
	console := newTestConsole(t, fake, &recordingWriter{}, &fakeTerminal{isTTY: false, width: 100}, "", false)
	console.refresh()
	before := len(console.state.actions)

	console.state.input = "r"
	console.submit()
	console.state.input = "8"
	console.submit()

	if fake.called("adopt:Device Service") || fake.called("terminate:Device Service") {
		t.Fatalf("a resolution ran without being chosen: %v", fake.callList())
	}
	if len(console.state.actions) != before {
		t.Fatalf("the offer changed while the operator did something else: %d -> %d items", before, len(console.state.actions))
	}
	if console.state.noticeLevel == noticeFail {
		t.Fatalf("doing nothing was recorded as a failure: %q", console.state.notice)
	}
}
