package adb

import (
	"sort"
	"strings"
	"testing"

	"drift.local/drift-next/internal/action"
)

// --- the two admissions must stay separable (card ARC-75 refinement) ---------
//
// `matchesAllowlist` serves two admissions that reach the same entry point an
// operator-authored input reaches: the six argument arrays the five typed device
// input builders produce (ADR-0010), and the read-only builders plus the
// render-size declaration read (ARC-75). Their separation is a safety property
// rather than a naming preference, so every half of it is asserted rather than
// argued:
//
//  1. the two recognisers never both match one argument array, so no
//     classification can depend on which one is asked first;
//  2. the operation names are disjoint, so one name never identifies two
//     different admissions;
//  3. the read's operation name is not an action-catalog kind, so it cannot be
//     selected as an operator action;
//  4. an array the input recogniser admits is reported under the input name: the
//     more specific admission is asked first, and asking twice answers the same.
//
// The corpus is every admitted array this adapter has, every near miss either
// admission refuses, and a bounded token-mutation cross-product around the
// admitted arrays, so an admission that collides with another is caught here
// rather than by care.

// readOnlyArrays is every array the read-only admission recognises, built from
// the same builders the adapter dispatches with.
func readOnlyArrays(t *testing.T) [][]string {
	t.Helper()
	getProp, err := getPropArgv("ro.build.version.sdk")
	if err != nil {
		t.Fatalf("getPropArgv() = %v", err)
	}
	dumpFile, err := UIAutomatorDumpFileArgv("/sdcard/drift-abc.xml")
	if err != nil {
		t.Fatalf("UIAutomatorDumpFileArgv() = %v", err)
	}
	cat, err := CatArgv("/sdcard/drift-abc.xml")
	if err != nil {
		t.Fatalf("CatArgv() = %v", err)
	}
	remove, err := RemoveArgv("/sdcard/drift-abc.xml")
	if err != nil {
		t.Fatalf("RemoveArgv() = %v", err)
	}
	return [][]string{
		renderSizeReadArgv(),
		getStateArgv(),
		screencapArgv(),
		UIAutomatorDumpStdoutArgv(),
		getProp,
		dumpFile,
		cat,
		remove,
	}
}

// inputArrays is every array the typed device input admission recognises: six
// arrays for five inputs, because an app launch has two shapes.
func inputArrays() [][]string {
	return [][]string{
		{"shell", "input", "tap", "540", "960"},
		{"shell", "input", "swipe", "1", "2", "3", "4", "300"},
		{"shell", "input", "keyevent", "4"},
		{"shell", "input", "text", "hello%sworld"},
		{"shell", "monkey", "-p", "com.example.app", "-c", "android.intent.category.LAUNCHER", "1"},
		{"shell", "am", "start", "-n", "com.example.app/.MainActivity"},
	}
}

// foreignTokens are drawn from both admissions' vocabularies, so mutating any
// position of any admitted array with one of them is the cheapest way to land on
// an array that both recognisers might claim.
var foreignTokens = []string{
	"input", "tap", "text", "keyevent", "monkey", "am",
	"wm", "size", "getprop", "uiautomator", "rm", "-f", "cat", "screencap",
	"shell", "exec-out", "get-state", "--compressed", "540", "com.example.app",
}

// classificationCorpus is everything this file reasons over: the admitted
// arrays, the near misses each admission refuses, and every one-token mutation
// of an admitted array (plus a truncated and an extended shape).
func classificationCorpus(t *testing.T) [][]string {
	t.Helper()
	corpus := make([][]string, 0, 512)
	admitted := append(readOnlyArrays(t), inputArrays()...)
	for _, args := range admitted {
		corpus = append(corpus, append([]string(nil), args...))
	}
	for _, nearMiss := range renderSizeReadNearMisses {
		corpus = append(corpus, append([]string(nil), nearMiss.args...))
	}
	for _, args := range [][]string{
		{"shell", "input", "tap", "540"},
		{"shell", "input", "tap", "540", "960", "1"},
		{"shell", "input", "tap", "-1", "960"},
		{"shell", "input", "tap", "0x21", "960"},
		{"shell", "input", "text", "100%"},
		{"shell", "input", "text", "a b"},
		{"shell", "input", "keyevent", "10001"},
		{"shell", "monkey", "-p", "com.example.app", "-c", "android.intent.category.LAUNCHER", "2"},
		{"shell", "am", "start", "-n", "com.example/../../data/data"},
		{"shell", "am", "start", "-a", "android.intent.action.VIEW"},
		{"shell", "sh", "-c", "wm size"},
		{"shell", "rm", "-rf", "/sdcard"},
		{"shell", "rm", "/sdcard/drift-abc.xml"},
		{"exec-out", "cat", "/data/misc/adb/adb_keys"},
		{"shell", "getprop", "ro.serialno"},
		{"devices", "-l"},
		{"version"},
		{"reconnect"},
	} {
		corpus = append(corpus, args)
	}
	for _, args := range admitted {
		for index := range args {
			for _, token := range foreignTokens {
				if args[index] == token {
					continue
				}
				mutated := append([]string(nil), args...)
				mutated[index] = token
				corpus = append(corpus, mutated)
			}
		}
		corpus = append(corpus, append(append([]string(nil), args...), "extra"))
		if len(args) > 1 {
			corpus = append(corpus, append([]string(nil), args[:len(args)-1]...))
		}
	}
	return corpus
}

// TestTheInputAndReadOnlyRecognisersNeverBothMatch is claim 1 and claim 4. Every
// array in the corpus is put to both recognisers directly: an array both of them
// admit would make the classification of that array depend on which recogniser
// happened to be asked first, which is exactly the surface this refinement is
// asked to make non-collidable.
func TestTheInputAndReadOnlyRecognisersNeverBothMatch(t *testing.T) {
	corpus := classificationCorpus(t)
	inputNames := map[string]string{}
	readOnlyNames := map[string]string{}

	for _, args := range corpus {
		inputName, inputOK := matchesDeviceInputAllowlist(args)
		readOnlyName, readOnlyOK := matchesReadOnlyAllowlist(args)
		if inputOK && readOnlyOK {
			t.Fatalf("both recognisers admit %q: input=%q read-only=%q; this array's classification would depend on which recogniser is asked first",
				args, inputName, readOnlyName)
		}
		if inputOK {
			inputNames[inputName] = strings.Join(args, " ")
		}
		if readOnlyOK {
			readOnlyNames[readOnlyName] = strings.Join(args, " ")
		}

		first, firstOK := matchesAllowlist(args)
		second, secondOK := matchesAllowlist(args)
		if first != second || firstOK != secondOK {
			t.Fatalf("matchesAllowlist(%q) is not deterministic: %q,%t then %q,%t", args, first, firstOK, second, secondOK)
		}
		switch {
		case inputOK:
			if !firstOK || first != inputName {
				t.Fatalf("matchesAllowlist(%q) = %q, %t; want the input classification %q, true: the more specific admission is asked first", args, first, firstOK, inputName)
			}
		case readOnlyOK:
			if !firstOK || first != readOnlyName {
				t.Fatalf("matchesAllowlist(%q) = %q, %t; want the read-only classification %q, true", args, first, firstOK, readOnlyName)
			}
		default:
			if firstOK {
				t.Fatalf("matchesAllowlist(%q) = %q, true; neither recogniser admits it", args, first)
			}
		}
	}

	// The corpus must actually exercise both admissions, or it proves nothing.
	if len(inputNames) != 6 {
		t.Fatalf("the corpus reached %d input operation names (%v), want all 6", len(inputNames), sortedKeys(inputNames))
	}
	if len(readOnlyNames) != 8 {
		t.Fatalf("the corpus reached %d read-only operation names (%v), want all 8", len(readOnlyNames), sortedKeys(readOnlyNames))
	}
}

// TestTheTwoAdmissionsNeverShareAnOperationName is claim 2: a name identifies one
// admission and one only, so a name observed anywhere cannot be misread as the
// other admission's classification.
func TestTheTwoAdmissionsNeverShareAnOperationName(t *testing.T) {
	inputNames := map[string]string{}
	for _, args := range inputArrays() {
		name, ok := matchesDeviceInputAllowlist(args)
		if !ok {
			t.Fatalf("matchesDeviceInputAllowlist(%q) = %q, false; want the input admission", args, name)
		}
		inputNames[name] = strings.Join(args, " ")
	}
	if len(inputNames) != 6 {
		t.Fatalf("the input admission reports %d operation names (%v), want 6", len(inputNames), sortedKeys(inputNames))
	}

	readOnlyNames := map[string]string{}
	for _, args := range readOnlyArrays(t) {
		name, ok := matchesReadOnlyAllowlist(args)
		if !ok {
			t.Fatalf("matchesReadOnlyAllowlist(%q) = %q, false; want the read-only admission", args, name)
		}
		readOnlyNames[name] = strings.Join(args, " ")
	}
	if len(readOnlyNames) != 8 {
		t.Fatalf("the read-only admission reports %d operation names (%v), want 8", len(readOnlyNames), sortedKeys(readOnlyNames))
	}

	for name, array := range inputNames {
		if other, clash := readOnlyNames[name]; clash {
			t.Fatalf("operation name %q identifies both the input array %q and the read-only array %q", name, array, other)
		}
	}
}

// TestTheRenderSizeReadIsNotAnOperatorAction is claim 3. The read's operation
// name is the allow-list's own vocabulary, never a catalog kind: no operator can
// select it as an action, and no catalog entry can be dispatched because this
// array was admitted.
func TestTheRenderSizeReadIsNotAnOperatorAction(t *testing.T) {
	name, ok := matchesAllowlist(renderSizeReadArgv())
	if !ok || name != renderSizeReadOperation {
		t.Fatalf("matchesAllowlist(%q) = %q, %t; want %q, true", renderSizeReadArgv(), name, ok, renderSizeReadOperation)
	}
	if spec, found := action.Lookup(action.Kind(name)); found {
		t.Fatalf("the allow-list operation name %q resolves to catalog kind %q; the render-size read must not be selectable as an operator action", name, spec.Kind)
	}
	for _, spec := range action.Catalog() {
		if string(spec.Kind) == name {
			t.Fatalf("catalog kind %q collides with the render-size read's operation name %q: this array would be dispatchable as an operator action", spec.Kind, name)
		}
	}
	// The lookup must be live, or both assertions above would hold for any name
	// at all and this test would prove nothing.
	if _, found := action.Lookup(action.Tap); !found {
		t.Fatal("action.Lookup does not resolve an existing catalog kind, so the assertions above are vacuous")
	}
	if inputName, inputOK := matchesDeviceInputAllowlist(renderSizeReadArgv()); inputOK {
		t.Fatalf("the render-size read is classified as the typed device input %q; nothing may map this read to an input kind", inputName)
	}
}

// sortedKeys is a deterministic rendering of the operation-name sets above, so a
// failure message lists them in a stable order.
func sortedKeys(names map[string]string) []string {
	keys := make([]string, 0, len(names))
	for name := range names {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys
}
