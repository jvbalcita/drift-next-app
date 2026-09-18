package adb

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateSerialRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name   string
		serial string
		want   error
	}{
		{name: "empty", serial: "", want: ErrSerialRequired},
		{name: "whitespace only", serial: "   ", want: ErrSerialRequired},
		{name: "tab only", serial: "\t", want: ErrSerialRequired},
		{name: "leading space", serial: " R5CT30ABCD", want: ErrSerialInvalid},
		{name: "trailing newline", serial: "R5CT30ABCD\n", want: ErrSerialInvalid},
		{name: "embedded newline", serial: "R5CT30\nABCD", want: ErrSerialInvalid},
		{name: "semicolon", serial: "R5CT30;rm", want: ErrSerialInvalid},
		{name: "pipe", serial: "R5CT30|cat", want: ErrSerialInvalid},
		{name: "ampersand", serial: "R5CT30&whoami", want: ErrSerialInvalid},
		{name: "dollar expansion", serial: "R5CT30$HOME", want: ErrSerialInvalid},
		{name: "backtick", serial: "R5CT30`id`", want: ErrSerialInvalid},
		{name: "single quote", serial: "R5CT30'", want: ErrSerialInvalid},
		{name: "double quote", serial: `R5CT30"`, want: ErrSerialInvalid},
		{name: "redirect", serial: "R5CT30>out", want: ErrSerialInvalid},
		{name: "subshell", serial: "R5CT30$(id)", want: ErrSerialInvalid},
		{name: "backslash", serial: `R5CT30\x`, want: ErrSerialInvalid},
		{name: "nul byte", serial: "R5CT30\x00", want: ErrSerialInvalid},
		{name: "leading dash looks like a flag", serial: "-s", want: ErrSerialInvalid},
		{name: "over long", serial: strings.Repeat("a", maxSerialLength+1), want: ErrSerialInvalid},
		{name: "non ascii", serial: "R5CT30é", want: ErrSerialInvalid},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSerial(test.serial)
			if !errors.Is(err, test.want) {
				t.Fatalf("ValidateSerial(%q) = %v, want %v", test.serial, err, test.want)
			}
		})
	}
}

func TestValidateSerialAcceptsRealShapes(t *testing.T) {
	for _, serial := range []string{
		"emulator-5554",
		"R5CT30ABCD",
		"192.168.1.10:5555",
		"0123456789ABCDEF",
		"ZY223K_9TD",
	} {
		if err := ValidateSerial(serial); err != nil {
			t.Fatalf("ValidateSerial(%q) = %v, want nil", serial, err)
		}
	}
}

func TestValidateExecutableRequiresExplicitAbsolutePath(t *testing.T) {
	for _, executable := range []string{"", "   ", "adb", "./adb", "bin/adb", "/usr/bin/adb\n"} {
		if err := validateExecutable(executable); !errors.Is(err, ErrExecutableRequired) {
			t.Fatalf("validateExecutable(%q) = %v, want ErrExecutableRequired", executable, err)
		}
	}
	if err := validateExecutable("/usr/local/bin/adb"); err != nil {
		t.Fatalf("validateExecutable(absolute) = %v, want nil", err)
	}
}

// TestBuiltArgvContainsNoShellMetacharacters asserts that every builder emits
// discrete tokens. A metacharacter can only appear if a value was concatenated
// into a token, which is exactly what this adapter forbids.
func TestBuiltArgvContainsNoShellMetacharacters(t *testing.T) {
	dumpFile, err := UIAutomatorDumpFileArgv("/sdcard/drift-abc123.xml")
	if err != nil {
		t.Fatalf("UIAutomatorDumpFileArgv() = %v", err)
	}
	cat, err := CatArgv("/sdcard/drift-abc123.xml")
	if err != nil {
		t.Fatalf("CatArgv() = %v", err)
	}
	remove, err := RemoveArgv("/sdcard/drift-abc123.xml")
	if err != nil {
		t.Fatalf("RemoveArgv() = %v", err)
	}
	getProp, err := getPropArgv("ro.product.model")
	if err != nil {
		t.Fatalf("getPropArgv() = %v", err)
	}

	argvs := map[string][]string{
		"devices":      devicesArgv(),
		"version":      versionArgv(),
		"get-state":    getStateArgv(),
		"screencap":    screencapArgv(),
		"device-name":  deviceNameArgv(),
		"getprop":      getProp,
		"dump-stdout":  UIAutomatorDumpStdoutArgv(),
		"dump-file":    dumpFile,
		"cat":          cat,
		"rm":           remove,
		"with-serial":  append([]string{"-s", "emulator-5554"}, getStateArgv()...),
		"tcp-serial":   append([]string{"-s", "192.168.1.10:5555"}, screencapArgv()...),
		"device-props": append([]string{"-s", "R5CT30ABCD"}, getProp...),
	}

	for name, argv := range argvs {
		t.Run(name, func(t *testing.T) {
			if err := validateArgv(argv); err != nil {
				t.Fatalf("validateArgv(%q) = %v, want nil", argv, err)
			}
			for _, token := range argv {
				if strings.ContainsAny(token, forbiddenArgRunes) {
					t.Fatalf("token %q contains a shell metacharacter", token)
				}
			}
			if strings.Contains(strings.Join(argv, " "), "sh -c") {
				t.Fatalf("argv %q invokes a shell interpreter", argv)
			}
		})
	}
}

func TestValidateArgvRejectsInjectedTokens(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{},
		{""},
		{"shell", "getprop ro.product.model"},
		{"shell", "getprop; id"},
		{"shell", "$(id)"},
		{"shell", "a\nb"},
		{"shell", "a\x00b"},
	} {
		if err := validateArgv(args); err == nil {
			t.Fatalf("validateArgv(%q) = nil, want an error", args)
		}
	}
}

func TestGetPropArgvHonorsTypedAllowlist(t *testing.T) {
	for _, property := range AllowedProperties() {
		args, err := getPropArgv(property)
		if err != nil {
			t.Fatalf("getPropArgv(%q) = %v, want nil", property, err)
		}
		if len(args) != 3 || args[0] != "shell" || args[1] != "getprop" || args[2] != property {
			t.Fatalf("getPropArgv(%q) = %q", property, args)
		}
	}
	for _, property := range []string{"", "ro.boot.serialno", "sys.boot_completed", "ro.product.model;id"} {
		if _, err := getPropArgv(property); !errors.Is(err, ErrPropertyNotAllowlisted) {
			t.Fatalf("getPropArgv(%q) = %v, want ErrPropertyNotAllowlisted", property, err)
		}
	}
}

func TestDevicePathStaysInsideTheAdapterNamespace(t *testing.T) {
	path, err := DevicePath("a1b2c3d4-0000-4000-8000-abcdefabcdef")
	if err != nil {
		t.Fatalf("DevicePath() = %v", err)
	}
	if !strings.HasPrefix(path, devicePathPrefix) || !strings.HasSuffix(path, devicePathSuffix) {
		t.Fatalf("DevicePath() = %q", path)
	}

	for _, candidate := range []string{
		"/sdcard/window_dump.xml",
		"/sdcard/drift-../../data/data/x.xml",
		"/data/local/tmp/drift-a.xml",
		"/sdcard/drift-a b.xml",
		"/sdcard/drift-a.xml.sh",
		"/sdcard/drift-.xml",
		"",
	} {
		if err := ValidateDevicePath(candidate); !errors.Is(err, ErrDevicePathInvalid) {
			t.Fatalf("ValidateDevicePath(%q) = %v, want ErrDevicePathInvalid", candidate, err)
		}
	}
	for _, correlationID := range []string{"", "../escape", "a b", "a;id"} {
		if _, err := DevicePath(correlationID); !errors.Is(err, ErrDevicePathInvalid) {
			t.Fatalf("DevicePath(%q) = %v, want ErrDevicePathInvalid", correlationID, err)
		}
	}
}

// TestAllowlistRejectsBlindReplayAndArbitraryShell documents that the adapter
// has no generic command pass-through: only builder-shaped arrays execute. The
// five typed device input builders are the one admission (ADR-0010); their
// near misses and every command-shaped array are refused.
func TestAllowlistRejectsBlindReplayAndArbitraryShell(t *testing.T) {
	for _, args := range [][]string{
		{"shell", "input", "tap", "540"},
		{"shell", "input", "tap", "540", "960", "1"},
		{"shell", "input", "text", "a;id"},
		{"shell", "am", "start", "-a", "android.intent.action.VIEW"},
		{"shell", "pm", "uninstall", "com.example"},
		{"shell", "rm", "-rf", "/sdcard"},
		{"exec-out", "sh"},
		{"sh", "-c", "ls"},
		{"shell", "sh", "-c", "ls"},
		{"exec-out", "cat", "/data/misc/adb/adb_keys"},
		// A build property outside the typed allow-list: the getprop admission
		// is bounded by that list, so a sibling of an admitted property is
		// still refused.
		{"shell", "getprop", "ro.boot.serialno"},
		{"shell", "uiautomator", "dump", "--compressed", "/sdcard/window_dump.xml"},
		{"push", "/tmp/payload", "/data/local/tmp/payload"},
		{"root"},
		{"devices", "-l"},
		{"version"},
	} {
		if name, ok := matchesAllowlist(args); ok {
			t.Fatalf("matchesAllowlist(%q) = %q, true; want false", args, name)
		}
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
	getProp, err := getPropArgv("ro.build.version.sdk")
	if err != nil {
		t.Fatalf("getPropArgv() = %v", err)
	}

	allowed := map[string][]string{
		"get-state":               getStateArgv(),
		"screencap":               screencapArgv(),
		"settings-device-name":    deviceNameArgv(),
		"getprop":                 getProp,
		"uiautomator-dump-stdout": UIAutomatorDumpStdoutArgv(),
		"uiautomator-dump-file":   dumpFile,
		"cat":                     cat,
		"rm":                      remove,
	}
	for want, args := range allowed {
		name, ok := matchesAllowlist(args)
		if !ok || name != want {
			t.Fatalf("matchesAllowlist(%q) = %q, %t; want %q, true", args, name, ok, want)
		}
	}
}

func TestRedactOutputRemovesSensitiveMaterial(t *testing.T) {
	stderr := []byte(strings.Join([]string{
		"adb: error: failed to authenticate",
		"tried key /Users/operator/.android/adbkey and /Users/operator/.android/adbkey.pub",
		"ADB_VENDOR_KEYS=/opt/vendor/TEST_ONLY_private_keys",
		"api_key=TEST_ONLY_sk_live_9f8e7d6c5b4a",
		"authorization: Bearer TEST_ONLY_eyJhbGciOiJIUzI1NiJ9padding",
		"session_token: 'TEST_ONLY_abcd_1234_secret'",
	}, "\n"))

	redacted := RedactOutput(stderr)

	for _, leaked := range []string{
		"adbkey",
		"TEST_ONLY_sk_live_9f8e7d6c5b4a",
		"TEST_ONLY_eyJhbGciOiJIUzI1NiJ9padding",
		"TEST_ONLY_abcd_1234_secret",
		"TEST_ONLY_private_keys",
	} {
		if strings.Contains(redacted, leaked) {
			t.Fatalf("RedactOutput() leaked %q:\n%s", leaked, redacted)
		}
	}
	if !strings.Contains(redacted, "failed to authenticate") {
		t.Fatalf("RedactOutput() dropped the diagnostic text:\n%s", redacted)
	}
}

func TestRedactOutputIsBounded(t *testing.T) {
	redacted := RedactOutput([]byte(strings.Repeat("x", maxDetailLength*3)))
	if len(redacted) <= maxDetailLength {
		t.Fatalf("RedactOutput() length = %d, want a bounded but non-empty value", len(redacted))
	}
	if !strings.HasSuffix(redacted, "[truncated]") {
		t.Fatalf("RedactOutput() did not mark truncation: %q", redacted[len(redacted)-32:])
	}
	if RedactOutput(nil) != "" {
		t.Fatal("RedactOutput(nil) should be empty")
	}
}
