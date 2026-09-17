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

// hostArrays are the connection-management admissions. They are part of the same
// corpus as every other admitted array, so a mutation of any of their positions
// is exercised by the same near-miss loop and a collision with another admission
// is caught by the same disjointness assertion.
func hostArrays(t *testing.T) [][]string {
	t.Helper()
	connect, err := ConnectArgv("192.168.1.109:5555")
	if err != nil {
		t.Fatalf("ConnectArgv() = %v", err)
	}
	return [][]string{connect, KillServerArgv(), StartServerArgv()}
}

// settingsArrays is every array the catalogued device-settings admission
// recognises: the five writes the two settings issue and the four read-backs
// they verify their own postconditions with (ARC-137, ADR-0016). Every token is
// spelled out here rather than imported from the builder, because the allow-list
// is an independent second gate and a property asserted against the builder's own
// output would prove nothing about the gate.
func settingsArrays() [][]string {
	return [][]string{
		{"shell", "settings", "put", "system", "accelerometer_rotation", "0"},
		{"shell", "settings", "put", "system", "user_rotation", "0"},
		{"shell", "settings", "delete", "secure", "autofill_service"},
		{"shell", "cmd", "autofill", "set", "default-augmented-service-enabled", "0", "false"},
		{"shell", "cmd", "autofill", "reset"},
		{"shell", "settings", "get", "system", "accelerometer_rotation"},
		{"shell", "settings", "get", "system", "user_rotation"},
		{"shell", "settings", "get", "secure", "autofill_service"},
		{"shell", "cmd", "autofill", "get", "default-augmented-service-enabled"},
	}
}

// settingsNearMisses are the arrays closest to the settings admission that it
// must refuse: the same shapes with one token changed to something a device
// would read as a different command, a different value, or a second command.
var settingsNearMisses = [][]string{
	// The same subcommand with the other value: a setting written to 1 is
	// exactly the change this admission exists to prevent.
	{"shell", "settings", "put", "system", "accelerometer_rotation", "1"},
	{"shell", "settings", "put", "system", "user_rotation", "1"},
	// A namespace, a key or a value the reviewed operation does not name.
	{"shell", "settings", "put", "secure", "accelerometer_rotation", "0"},
	{"shell", "settings", "put", "system", "auto_rotate", "0"},
	{"shell", "settings", "put", "system", "accelerometer_rotation"},
	{"shell", "settings", "put", "system", "accelerometer_rotation", "0", "1"},
	{"shell", "settings", "delete", "secure", "autofill_service", "extra"},
	{"shell", "settings", "delete", "system", "autofill_service"},
	{"shell", "cmd", "autofill", "set", "default-augmented-service-enabled", "0", "true"},
	{"shell", "cmd", "autofill", "set", "default-augmented-service-enabled", "1", "false"},
	{"shell", "cmd", "autofill", "set", "default-augmented-service-enabled", "0"},
	{"shell", "cmd", "autofill", "reset", "extra"},
	{"shell", "cmd", "autofill", "disable"},
	// A read with a second command attached, or a second position.
	{"shell", "settings", "get", "system", "accelerometer_rotation", "extra"},
	{"shell", "settings", "get", "system"},
	{"shell", "cmd", "autofill", "get", "default-augmented-service-enabled", "0"},
	{"shell", "settings", "list", "system"},
	// A different binary, a shell wrapper, a flag, a path or a second command.
	{"sh", "settings", "get", "system", "user_rotation"},
	{"shell", "sh", "-c", "settings get system user_rotation"},
	{"shell", "settings", "--help"},
	{"shell", "settings", "get", "system", "/data/local/tmp/user_rotation"},
	{"shell", "settings", "get", "system", "user_rotation; id"},
	{"shell", "settings", "get", "system", "user_rotation && id"},
	{"shell", "settings", "get", "system", "user_rotation|id"},
	{"shell", "settings", "get", "system", "$(id)"},
}

// transportArrays are the device transport-mode admissions. They are kept apart from
// the host arrays deliberately: `tcpip` acts on a device and is reached only through
// the serial-bound entry point, so it must not be admitted where no serial is named.
func transportArrays(t *testing.T) [][]string {
	t.Helper()
	tcpip, err := TcpipArgv(5556)
	if err != nil {
		t.Fatalf("TcpipArgv() = %v", err)
	}
	return [][]string{tcpip}
}

// admissionFamily is one admission the adapter serves, with the recogniser a test
// can ask independently and the arrays that admission is expected to recognise.
// The counts are part of the expectation: an admission that stopped recognising
// one of its own shapes would otherwise leave the corpus assertion passing while
// proving less.
type admissionFamily struct {
	name      string
	recognise func([]string) (string, bool)
	arrays    [][]string
	wantNames int
}

// admissionFamilies is every admission, in the order matchesAllowlist asks them.
// The separation between them is a safety property, so it is asserted over the
// whole set rather than over the pairs someone remembered to enumerate.
func admissionFamilies(t *testing.T) []admissionFamily {
	t.Helper()
	return []admissionFamily{
		{"input", matchesDeviceInputAllowlist, inputArrays(), 6},
		{"settings", matchesDeviceSettingsAllowlist, settingsArrays(), 9},
		{"read-only", matchesReadOnlyAllowlist, readOnlyArrays(t), 8},
		{"host", matchesHostAllowlist, hostArrays(t), 3},
		{"transport", matchesTransportAllowlist, transportArrays(t), 1},
	}
}

// foreignTokens are drawn from all admissions' vocabularies, so mutating any
// position of any admitted array with one of them is the cheapest way to land on
// an array that two recognisers might claim.
var foreignTokens = []string{
	"input", "tap", "text", "keyevent", "monkey", "am",
	"wm", "size", "getprop", "uiautomator", "rm", "-f", "cat", "screencap",
	"shell", "exec-out", "get-state", "--compressed", "540", "com.example.app",
	"settings", "put", "get", "delete", "system", "secure", "accelerometer_rotation",
	"user_rotation", "autofill_service", "cmd", "autofill", "reset", "default-augmented-service-enabled",
}

// classificationCorpus is everything this file reasons over: the admitted
// arrays, the near misses each admission refuses, and every one-token mutation
// of an admitted array (plus a truncated and an extended shape).
func classificationCorpus(t *testing.T) [][]string {
	t.Helper()
	corpus := make([][]string, 0, 1024)
	admitted := make([][]string, 0, 64)
	for _, family := range admissionFamilies(t) {
		admitted = append(admitted, family.arrays...)
	}
	for _, args := range admitted {
		corpus = append(corpus, append([]string(nil), args...))
	}
	for _, nearMiss := range renderSizeReadNearMisses {
		corpus = append(corpus, append([]string(nil), nearMiss.args...))
	}
	for _, nearMiss := range settingsNearMisses {
		corpus = append(corpus, append([]string(nil), nearMiss...))
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

// TestTheAdmissionsNeverBothMatch is claim 1 and claim 4. Every array in the
// corpus is put to every recogniser directly: an array two of them admit would
// make the classification of that array depend on which recogniser happened to be
// asked first, which is exactly the surface this refinement is asked to make
// non-collidable. Because the families are disjoint, the name the combined entry
// point reports is also asserted to be the admitting family's own name, which is
// an order-independent statement rather than a restatement of the pinned order.
func TestTheAdmissionsNeverBothMatch(t *testing.T) {
	families := admissionFamilies(t)
	corpus := classificationCorpus(t)
	names := make([]map[string]string, len(families))
	for index := range names {
		names[index] = map[string]string{}
	}

	for _, args := range corpus {
		claimed := -1
		claimedName := ""
		for index, family := range families {
			name, ok := family.recognise(args)
			if !ok {
				continue
			}
			if claimed >= 0 {
				t.Fatalf("more than one recogniser admits %q: %s as %q and %s as %q; this array's classification would depend on which recogniser is asked first",
					args, families[claimed].name, claimedName, family.name, name)
			}
			claimed, claimedName = index, name
		}

		first, firstOK := matchesAllowlist(args)
		second, secondOK := matchesAllowlist(args)
		if first != second || firstOK != secondOK {
			t.Fatalf("matchesAllowlist(%q) is not deterministic: %q,%t then %q,%t", args, first, firstOK, second, secondOK)
		}
		if claimed < 0 {
			if firstOK {
				t.Fatalf("matchesAllowlist(%q) = %q, true; no recogniser admits it", args, first)
			}
			continue
		}
		names[claimed][claimedName] = strings.Join(args, " ")
		if !firstOK || first != claimedName {
			t.Fatalf("matchesAllowlist(%q) = %q, %t; want the %s classification %q, true", args, first, firstOK, families[claimed].name, claimedName)
		}
	}

	// The corpus must actually exercise every admission, or it proves nothing.
	for index, family := range families {
		if len(names[index]) != family.wantNames {
			t.Fatalf("the corpus reached %d %s operation names (%v), want all %d", len(names[index]), family.name, sortedKeys(names[index]), family.wantNames)
		}
	}
}

// TestNoTwoAdmissionsShareAnOperationName is claim 2: a name identifies one
// admission and one only, so a name observed anywhere cannot be misread as
// another admission's classification.
func TestNoTwoAdmissionsShareAnOperationName(t *testing.T) {
	families := admissionFamilies(t)
	names := make([]map[string]string, len(families))
	for index, family := range families {
		names[index] = map[string]string{}
		for _, args := range family.arrays {
			name, ok := family.recognise(args)
			if !ok {
				t.Fatalf("the %s admission does not admit its own array %q", family.name, args)
			}
			names[index][name] = strings.Join(args, " ")
		}
		if len(names[index]) != family.wantNames {
			t.Fatalf("the %s admission reports %d operation names (%v), want %d", family.name, len(names[index]), sortedKeys(names[index]), family.wantNames)
		}
	}
	for left := 0; left < len(families); left++ {
		for right := left + 1; right < len(families); right++ {
			for name, array := range names[left] {
				if other, clash := names[right][name]; clash {
					t.Fatalf("operation name %q identifies both the %s array %q and the %s array %q", name, families[left].name, array, families[right].name, other)
				}
			}
		}
	}
}

// TestNoAdmissionOperationNameIsAnOperatorAction is claim 3, asserted over every
// admission rather than over the read-only one alone. The allow-list's operation
// names are its own vocabulary, never catalog kinds: no operator can select one
// as an action, and no catalog entry can be dispatched because an array was
// admitted.
func TestNoAdmissionOperationNameIsAnOperatorAction(t *testing.T) {
	kinds := map[string]bool{}
	for _, spec := range action.Catalog() {
		kinds[string(spec.Kind)] = true
	}
	// The lookup must be live, or every assertion below would hold for any name
	// at all and this test would prove nothing.
	if !kinds[string(action.Tap)] {
		t.Fatal("the catalog does not declare the tap kind, so the assertions below are vacuous")
	}
	for _, family := range admissionFamilies(t) {
		for _, args := range family.arrays {
			name, ok := family.recognise(args)
			if !ok {
				t.Fatalf("the %s admission does not admit its own array %q", family.name, args)
			}
			if kinds[name] {
				t.Fatalf("the %s operation name %q is also an action-catalog kind, so this array would be dispatchable as an operator action", family.name, name)
			}
			if spec, found := action.Lookup(action.Kind(name)); found {
				t.Fatalf("the %s operation name %q resolves to catalog kind %q", family.name, name, spec.Kind)
			}
		}
	}
}

// TestTheRenderSizeReadIsNotAnOperatorAction is the original form of claim 3, kept
// for the render-size read specifically: it asserts the read is not classified as
// a typed device input either, which the generic loop above cannot express.
func TestTheRenderSizeReadIsNotAnOperatorAction(t *testing.T) {
	name, ok := matchesAllowlist(renderSizeReadArgv())
	if !ok || name != renderSizeReadOperation {
		t.Fatalf("matchesAllowlist(%q) = %q, %t; want %q, true", renderSizeReadArgv(), name, ok, renderSizeReadOperation)
	}
	if inputName, inputOK := matchesDeviceInputAllowlist(renderSizeReadArgv()); inputOK {
		t.Fatalf("the render-size read is classified as the typed device input %q; nothing may map this read to an input kind", inputName)
	}
	if settingsName, settingsOK := matchesDeviceSettingsAllowlist(renderSizeReadArgv()); settingsOK {
		t.Fatalf("the render-size read is classified as the device setting %q; nothing may map this read to a settings operation", settingsName)
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
