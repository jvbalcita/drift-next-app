package adb

import (
	"strings"
	"testing"
)

// TestAllowlistAdmitsExactlyTheFiveDeviceInputBuilderShapes pins the narrow
// admission ADR-0010 records: the allow-list accepts the argument arrays the
// five typed device input builders produce, and nothing else.
//
// The five inputs produce six arrays because an app launch has two shapes: a
// package-only launch and a launch that names one activity component.
func TestAllowlistAdmitsExactlyTheFiveDeviceInputBuilderShapes(t *testing.T) {
	tests := []struct {
		want string
		args []string
	}{
		{want: "input-tap", args: []string{"shell", "input", "tap", "540", "960"}},
		{want: "input-tap", args: []string{"shell", "input", "tap", "0", "0"}},
		{want: "input-tap", args: []string{"shell", "input", "tap", "9999", "9999"}},
		{want: "input-swipe", args: []string{"shell", "input", "swipe", "1", "2", "3", "4", "300"}},
		{want: "input-swipe", args: []string{"shell", "input", "swipe", "9999", "9999", "0", "0", "300000"}},
		{want: "input-keyevent", args: []string{"shell", "input", "keyevent", "4"}},
		{want: "input-keyevent", args: []string{"shell", "input", "keyevent", "10000"}},
		{want: "input-text", args: []string{"shell", "input", "text", "hello%sworld"}},
		{want: "input-text", args: []string{"shell", "input", "text", "hello"}},
		{want: "launch-app-package", args: []string{"shell", "monkey", "-p", "com.example.app", "-c", "android.intent.category.LAUNCHER", "1"}},
		{want: "launch-app-activity", args: []string{"shell", "am", "start", "-n", "com.example.app/.MainActivity"}},
		{want: "launch-app-activity", args: []string{"shell", "am", "start", "-n", "com.example.app/com.example.app.Main"}},
		{want: "launch-app-activity", args: []string{"shell", "am", "start", "-n", "com.example.app/._Main2"}},
	}
	for _, test := range tests {
		name, ok := matchesAllowlist(test.args)
		if !ok || name != test.want {
			t.Fatalf("matchesAllowlist(%q) = %q, %t; want %q, true", test.args, name, ok, test.want)
		}
	}
}

// TestAllowlistRefusesCommandShapedAndMalformedInputArrays is the other half of
// the admission: a near miss that could express a command string, a second
// command, a flag, a path, or an out-of-bound parameter is refused.
func TestAllowlistRefusesCommandShapedAndMalformedInputArrays(t *testing.T) {
	for _, args := range [][]string{
		// A second command, a separator, a substitution, or a redirect.
		{"shell", "input", "tap", "540", "960; id"},
		{"shell", "input", "tap", "540", "960 && rm -rf /sdcard"},
		{"shell", "input", "tap", "540", "960|id"},
		{"shell", "input", "tap", "540", "$(id)"},
		{"shell", "input", "tap", "540", "`id`"},
		{"shell", "input", "tap", "540", "960", ">", "/sdcard/x"},
		{"shell", "input", "tap", "540", "960", "&&", "id"},
		// Wrong arity.
		{"shell", "input", "tap", "540"},
		{"shell", "input", "tap", "540", "960", "1"},
		{"shell", "input", "swipe", "1", "2", "3", "4"},
		{"shell", "input", "swipe", "1", "2", "3", "4", "300", "extra"},
		{"shell", "input", "keyevent"},
		{"shell", "input", "keyevent", "4", "4"},
		{"shell", "input", "text"},
		{"shell", "input", "text", "a", "b"},
		{"shell", "am", "start", "-n", "com.example.app/.Main", "extra"},
		{"shell", "monkey", "-p", "com.example.app", "-c", "android.intent.category.LAUNCHER"},
		{"shell", "monkey", "-p", "com.example.app", "-c", "android.intent.category.LAUNCHER", "1", "extra"},
		// A parameter that is not the builder's bounded decimal.
		{"shell", "input", "tap", "-1", "960"},
		{"shell", "input", "tap", "540", "-960"},
		{"shell", "input", "tap", "0x21", "960"},
		{"shell", "input", "tap", "01", "960"},
		{"shell", "input", "tap", " 540", "960"},
		{"shell", "input", "tap", "540.5", "960"},
		{"shell", "input", "tap", "10000", "960"},
		{"shell", "input", "tap", "99999999999999999999", "960"},
		{"shell", "input", "swipe", "1", "2", "3", "4", "0"},
		{"shell", "input", "swipe", "1", "2", "3", "4", "300001"},
		{"shell", "input", "keyevent", "0"},
		{"shell", "input", "keyevent", "10001"},
		// A text token that is not the builder's single-token escape.
		{"shell", "input", "text", ""},
		{"shell", "input", "text", "a b"},
		{"shell", "input", "text", "a\tb"},
		{"shell", "input", "text", "a\nb"},
		{"shell", "input", "text", "a;id"},
		{"shell", "input", "text", "$(id)"},
		{"shell", "input", "text", "100%"},
		{"shell", "input", "text", "%d"},
		{"shell", "input", "text", "%"},
		{"shell", "input", "text", "caf\u00e9"},
		{"shell", "input", "text", "a%sb%dc"},
		{"shell", "input", "text", strings.Repeat("a", maxArgTokenLength+1)},
		// A subcommand or flag the builders never emit.
		{"shell", "input", "source", "/sdcard/x"},
		{"shell", "input", "tap"}, // no coordinates at all
		{"shell", "input", "keyevent", "4", "5"},
		{"shell", "monkey", "-p", "com.example.app", "-c", "android.intent.category.SECONDARY_HOME", "1"},
		{"shell", "monkey", "-p", "com.example.app", "-c", "android.intent.category.LAUNCHER", "0"},
		{"shell", "monkey", "-p", "com.example.app", "-c", "android.intent.category.LAUNCHER", "2"},
		{"shell", "monkey", "-p", "com.example.app", "-c", "android.intent.category.LAUNCHER", "-1"},
		{"shell", "monkey", "-p", "not a package", "-c", "android.intent.category.LAUNCHER", "1"},
		{"shell", "monkey", "-p", "com.example.app"},
		{"shell", "am", "start", "-a", "android.intent.action.VIEW"},
		{"shell", "am", "start", "-n", "com.example"},
		{"shell", "am", "start", "-n", "com.example/"},
		{"shell", "am", "start", "-n", "/.Main"},
		{"shell", "am", "start", "-n", "com.example/../../data/data"},
		{"shell", "am", "start", "-n", "com.example/.Main; id"},
		{"shell", "am", "start", "-n", "com.example app/.Main"},
		{"shell", "am", "force-stop", "com.example.app"},
		{"shell", "am", "broadcast", "-a", "android.intent.action.BOOT_COMPLETED"},
		// A generic command surface.
		{"shell", "sh", "-c", "input tap 1 2"},
		{"shell", "sh"},
		{"shell"},
		{"exec-out", "input", "tap", "1", "2"},
		{"sh", "-c", "input tap 1 2"},
		{"shell", "input", "text", "a", "b", "c"},
	} {
		if name, ok := matchesAllowlist(args); ok {
			t.Fatalf("matchesAllowlist(%q) = %q, true; want false", args, name)
		}
	}
}

// TestInputTextTokenRefusesABarePercent pins the escape rule directly: the
// device input command's only space escape survives, and a literal percent is
// refused rather than typed as something other than itself.
func TestInputTextTokenRefusesABarePercent(t *testing.T) {
	for _, token := range []string{"hello%sworld", "%ss", "abc", "aBC123%s"} {
		if !isInputTextToken(token) {
			t.Fatalf("isInputTextToken(%q) = false, want true", token)
		}
	}
	for _, token := range []string{"", "100%", "%", "%d", "a%b"} {
		if isInputTextToken(token) {
			t.Fatalf("isInputTextToken(%q) = true, want false", token)
		}
	}
}
