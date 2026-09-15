package adb

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"drift.local/drift-next/internal/platform/redaction"
)

const (
	maxSerialLength   = 128
	maxArgTokenLength = 512
	maxDetailLength   = 2048

	// devicePathPrefix is the only device-side directory this adapter writes
	// to, and every file it creates there is removed after the operation.
	devicePathPrefix = "/sdcard/drift-"
	devicePathSuffix = ".xml"
)

var (
	// ErrExecutableRequired reports a missing or non-absolute adb path. The
	// executable is explicit configuration and is empty until an operator
	// sets it.
	ErrExecutableRequired = errors.New("adb executable path must be explicitly configured as an absolute path")

	// ErrSerialRequired reports an empty explicit serial.
	ErrSerialRequired = errors.New("adb device serial is required")

	// ErrSerialInvalid reports a serial that is not safe to pass as an argv
	// token.
	ErrSerialInvalid = errors.New("adb device serial contains unsupported characters")

	// ErrArgvInvalid reports an argument token that is not safe to execute.
	ErrArgvInvalid = errors.New("adb argument is not a safe argv token")

	// ErrArgvNotAllowlisted reports an argument array that no allowlist
	// builder can produce.
	ErrArgvNotAllowlisted = errors.New("adb argument array is not allowlisted")

	// ErrPropertyNotAllowlisted reports a getprop key outside the typed
	// allowlist.
	ErrPropertyNotAllowlisted = errors.New("adb property is not allowlisted")

	// ErrDevicePathInvalid reports a device-side path outside the adapter's
	// private temporary namespace.
	ErrDevicePathInvalid = errors.New("adb device path is not an adapter-owned temporary file")
)

// forbiddenArgRunes never appear in an allowlisted argv token. They are
// rejected as defense in depth even though no builder can emit them.
const forbiddenArgRunes = " \t\r\n;|&$`\\\"'<>(){}[]*?!~^"

var (
	serialPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	devicePathPattern = regexp.MustCompile(`^/sdcard/drift-[A-Za-z0-9-]{1,64}\.xml$`)

	adbKeyPathPattern = regexp.MustCompile(`(?i)[^\s"']*adbkey(?:\.pub)?[^\s"']*`)
	vendorKeysPattern = regexp.MustCompile(`(?i)(\badb_vendor_keys\s*[:=]\s*)[^\s]+`)
)

// allowedProperties is the typed allow-list of read-only build properties this
// adapter may read. No caller can widen it at runtime.
var allowedProperties = map[string]struct{}{
	"ro.build.version.release":        {},
	"ro.build.version.sdk":            {},
	"ro.build.version.security_patch": {},
	"ro.product.model":                {},
	"ro.product.manufacturer":         {},
	"ro.product.device":               {},
	"ro.product.cpu.abi":              {},
}

// AllowedProperties returns a sorted copy of the readable property allow-list.
func AllowedProperties() []string {
	keys := make([]string, 0, len(allowedProperties))
	for key := range allowedProperties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ValidateSerial rejects any serial that is empty, padded, over-long, or
// capable of changing argv meaning. It runs before every device operation.
func ValidateSerial(serial string) error {
	if serial == "" {
		return ErrSerialRequired
	}
	if strings.TrimSpace(serial) == "" {
		return fmt.Errorf("%w: serial is whitespace", ErrSerialRequired)
	}
	if strings.TrimSpace(serial) != serial {
		return fmt.Errorf("%w: serial has surrounding whitespace", ErrSerialInvalid)
	}
	if len(serial) > maxSerialLength {
		return fmt.Errorf("%w: serial exceeds %d bytes", ErrSerialInvalid, maxSerialLength)
	}
	if err := validateArgToken(serial); err != nil {
		return fmt.Errorf("%w: %s", ErrSerialInvalid, err)
	}
	if !serialPattern.MatchString(serial) {
		return ErrSerialInvalid
	}
	return nil
}

func validateExecutable(executable string) error {
	if strings.TrimSpace(executable) == "" || executable != strings.TrimSpace(executable) {
		return ErrExecutableRequired
	}
	if !filepath.IsAbs(executable) {
		return ErrExecutableRequired
	}
	if strings.ContainsAny(executable, "\x00\n\r") {
		return ErrExecutableRequired
	}
	return nil
}

func validateArgToken(token string) error {
	if token == "" {
		return fmt.Errorf("%w: empty token", ErrArgvInvalid)
	}
	if len(token) > maxArgTokenLength {
		return fmt.Errorf("%w: token exceeds %d bytes", ErrArgvInvalid, maxArgTokenLength)
	}
	for _, r := range token {
		if r < 0x20 || r > 0x7e {
			return fmt.Errorf("%w: token contains a non-printable byte", ErrArgvInvalid)
		}
		if strings.ContainsRune(forbiddenArgRunes, r) {
			return fmt.Errorf("%w: token contains %q", ErrArgvInvalid, r)
		}
	}
	return nil
}

func validateArgv(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: argument array is empty", ErrArgvInvalid)
	}
	for _, token := range args {
		if err := validateArgToken(token); err != nil {
			return err
		}
	}
	return nil
}

// ValidateDevicePath reports whether path is an adapter-owned temporary file
// on the device. Nothing else may be read, written, or removed.
func ValidateDevicePath(path string) error {
	if !devicePathPattern.MatchString(path) {
		return ErrDevicePathInvalid
	}
	if strings.Contains(path, "..") {
		return ErrDevicePathInvalid
	}
	return validateArgToken(path)
}

// DevicePath returns the adapter-owned temporary device path for a correlation
// identifier. The caller is responsible for removing it after the operation.
func DevicePath(correlationID string) (string, error) {
	if correlationID == "" {
		return "", ErrDevicePathInvalid
	}
	path := devicePathPrefix + correlationID + devicePathSuffix
	if err := ValidateDevicePath(path); err != nil {
		return "", err
	}
	return path, nil
}

// --- argv builders -------------------------------------------------------
//
// Every builder returns a fresh slice of fixed tokens. No builder accepts free
// text, and no builder concatenates a caller value into an existing token.

func devicesArgv() []string { return []string{"devices", "-l"} }

func versionArgv() []string { return []string{"version"} }

func getStateArgv() []string { return []string{"get-state"} }

func screencapArgv() []string { return []string{"exec-out", "screencap", "-p"} }

func getPropArgv(property string) ([]string, error) {
	if _, ok := allowedProperties[property]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrPropertyNotAllowlisted, property)
	}
	return []string{"shell", "getprop", property}, nil
}

// UIAutomatorDumpStdoutArgv streams a compressed hierarchy dump straight to
// stdout so no device-side temporary file is created.
func UIAutomatorDumpStdoutArgv() []string {
	return []string{"exec-out", "uiautomator", "dump", "--compressed", "/dev/tty"}
}

// UIAutomatorDumpFileArgv writes a compressed hierarchy dump to an
// adapter-owned temporary file that the caller must remove.
func UIAutomatorDumpFileArgv(devicePath string) ([]string, error) {
	if err := ValidateDevicePath(devicePath); err != nil {
		return nil, err
	}
	return []string{"shell", "uiautomator", "dump", "--compressed", devicePath}, nil
}

// CatArgv reads back an adapter-owned temporary file as bytes.
func CatArgv(devicePath string) ([]string, error) {
	if err := ValidateDevicePath(devicePath); err != nil {
		return nil, err
	}
	return []string{"exec-out", "cat", devicePath}, nil
}

// RemoveArgv deletes an adapter-owned temporary file.
func RemoveArgv(devicePath string) ([]string, error) {
	if err := ValidateDevicePath(devicePath); err != nil {
		return nil, err
	}
	return []string{"shell", "rm", "-f", devicePath}, nil
}

// matchesAllowlist reports the operation name for an argument array that a
// builder could have produced. Arrays that follow an explicit -s SERIAL are
// only executed when they match. Host-scoped commands (devices, version) are
// intentionally absent because they take no serial.
func matchesAllowlist(args []string) (string, bool) {
	switch {
	case equalArgv(args, getStateArgv()):
		return "get-state", true
	case equalArgv(args, screencapArgv()):
		return "screencap", true
	case equalArgv(args, UIAutomatorDumpStdoutArgv()):
		return "uiautomator-dump-stdout", true
	case len(args) == 3 && args[0] == "shell" && args[1] == "getprop" && isAllowedProperty(args[2]):
		return "getprop", true
	case len(args) == 5 && args[0] == "shell" && args[1] == "uiautomator" && args[2] == "dump" &&
		args[3] == "--compressed" && ValidateDevicePath(args[4]) == nil:
		return "uiautomator-dump-file", true
	case len(args) == 3 && args[0] == "exec-out" && args[1] == "cat" && ValidateDevicePath(args[2]) == nil:
		return "cat", true
	case len(args) == 4 && args[0] == "shell" && args[1] == "rm" && args[2] == "-f" && ValidateDevicePath(args[3]) == nil:
		return "rm", true
	default:
		return "", false
	}
}

func isAllowedProperty(property string) bool {
	_, ok := allowedProperties[property]
	return ok
}

func equalArgv(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// RedactOutput converts captured process output into a bounded, safe string
// for errors, audit records, and logs. Credential material, adb key paths, and
// vendor-key locations never survive.
func RedactOutput(output []byte) string {
	if len(output) == 0 {
		return ""
	}
	redacted := redaction.RedactString(string(output))
	redacted = adbKeyPathPattern.ReplaceAllString(redacted, redaction.Replacement)
	redacted = vendorKeysPattern.ReplaceAllString(redacted, `$1`+redaction.Replacement)
	redacted = strings.TrimSpace(redacted)
	if len(redacted) > maxDetailLength {
		redacted = redacted[:maxDetailLength] + "…[truncated]"
	}
	return redacted
}
