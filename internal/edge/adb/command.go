package adb

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

	// deviceInboxDir is the ONE device-side directory the catalogued file
	// operations this product offers can address. It is this product's own
	// directory rather than a shared one (Downloads, DCIM, the device's own
	// storage root), so a file operation can never write over something the
	// operator did not put there, and a read can never reach a file this
	// product did not place.
	deviceInboxDir = "/sdcard/drift-inbox"
	// maxDeviceFileNameLength bounds one file name inside that directory.
	maxDeviceFileNameLength = 64
	// maxTransferHostPathLength bounds one host-side transfer path.
	maxTransferHostPathLength = 512
	// transferHostDirName and transferHostFilePrefix name the ONE host-side
	// directory and file shape a catalogued push may read from. The plane
	// creates the file itself; the allow-list is the second gate that keeps
	// any other host path out of an admitted array.
	transferHostDirName      = "drift-transfer"
	transferHostFilePrefix   = "drift-transfer-"
	maxTransferHostFileToken = 64
)

// Bounds and fixed tokens for the typed device input admission. Every bound is
// re-derived here rather than imported from the input builder: the allow-list
// is an independent second gate, so a defect in one gate does not reach a
// device through the other.
const (
	// maxInputCoordinate is the largest render-space coordinate the contract
	// admits (a coordinate lies strictly inside a render space of at most
	// 10000x10000).
	maxInputCoordinate = 9999
	// maxInputSwipeDurationMillis bounds one swipe.
	maxInputSwipeDurationMillis = 300000
	// maxInputKeyCode bounds one key event.
	maxInputKeyCode = 10000
	// launcherCategory is the only component category an app launch may name.
	launcherCategory = "android.intent.category.LAUNCHER"
	// launcherCountToken is the fixed monkey invocation count an app launch
	// uses: exactly one launch.
	launcherCountToken = "1"
	// maxComponentLength bounds a package or component name.
	maxComponentLength = 255
	// maxImeComponentLength bounds one input-method component name.
	maxImeComponentLength = 255
)

var (
	// packageNamePattern and componentNamePattern are names, never command
	// text: a package is dotted identifiers, a component is identifiers joined
	// by dots or underscores.
	packageNamePattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$`)
	componentNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)

	// deviceFileNamePattern is a file NAME, never a path: it carries no
	// separator of any kind, so a name that matched it cannot address a
	// directory, a parent, a device-absolute path or a second file.
	deviceFileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// deviceInboxPathPattern is the ONE device-side path shape a catalogued
	// file operation composes from such a name. It lives in this adapter
	// rather than in the caller, so "which device paths are addressable" is a
	// question this gate answers for itself.
	deviceInboxPathPattern = regexp.MustCompile(`^/sdcard/drift-inbox/[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// transferHostPathPattern is the ONE host-side path shape a catalogued
	// push may carry: a file whose own name this product generated, inside a
	// directory of its own name. Anything else - a deployment path, a system
	// path, a home directory, another user's spool - is a different array and
	// stays refused.
	transferHostPathPattern = regexp.MustCompile(`^/(?:[A-Za-z0-9._-]+/)*drift-transfer/drift-transfer-[A-Za-z0-9-]{1,64}\.[A-Za-z0-9]{1,8}$`)
	// imeComponentPattern is an input-method component NAME: a dotted package,
	// a separator, and the service half of the component. The device's own
	// `ime list -s` answers in exactly this shape, and the plane only ever
	// writes a component it read from that list.
	imeComponentPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+/[A-Za-z0-9_.$]+$`)
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

	// ErrDeviceFileNameInvalid reports a file name that is not a bounded name:
	// it is empty, padded, over-long, or carries a separator, a parent or a
	// shell character.
	ErrDeviceFileNameInvalid = errors.New("adb device file name is not a bounded name inside this product's device directory")

	// ErrTransferHostPathInvalid reports a host-side path that is not a
	// transfer file this product created.
	ErrTransferHostPathInvalid = errors.New("host path is not a transfer file this product owns")

	// ErrImeComponentInvalid reports an input-method component that is not a
	// bounded component name.
	ErrImeComponentInvalid = errors.New("ime component is not a bounded component name")
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
//
// reportedSerialProperty is admitted for identity rather than for a health or
// settings read: a TCP transport's serial IS the address the device answers on,
// so the registry needs the serial the device reports for itself to keep one
// device on one identity when that address changes (AGENTS.md section 2). It is
// a fixed property read, and the adapter bounds and pattern-checks the value
// before anything uses it.
var allowedProperties = map[string]struct{}{
	"ro.build.version.release":        {},
	"ro.build.version.sdk":            {},
	"ro.build.version.security_patch": {},
	"ro.product.model":                {},
	"ro.product.manufacturer":         {},
	"ro.product.device":               {},
	"ro.product.cpu.abi":              {},
	reportedSerialProperty:            {},
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

// ValidateDeviceFileName reports whether name is a bounded file NAME rather
// than a path. It is the replacement for "let the caller tell us where to
// write": the name carries no separator, no parent, no root and no second
// component, so a name that passes this may only ever address one file inside
// the one directory this product composes the path from.
func ValidateDeviceFileName(name string) error {
	if name == "" || len(name) > maxDeviceFileNameLength || !deviceFileNamePattern.MatchString(name) {
		return ErrDeviceFileNameInvalid
	}
	if strings.Contains(name, "..") {
		return ErrDeviceFileNameInvalid
	}
	return validateArgToken(name)
}

// ValidateDeviceInboxPath reports whether path is a file inside the ONE device
// directory this product owns, named by a bounded file name. Nothing else -
// not Downloads, not a shared directory, not the device root, not the
// adapter's own dump namespace - is addressable by a catalogued file
// operation.
func ValidateDeviceInboxPath(path string) error {
	if len(path) > len(deviceInboxDir)+1+maxDeviceFileNameLength || !deviceInboxPathPattern.MatchString(path) {
		return ErrDevicePathInvalid
	}
	if strings.Contains(path, "..") {
		return ErrDevicePathInvalid
	}
	return validateArgToken(path)
}

// ValidateTransferHostPath reports whether path is a host-side transfer file
// this product created: a file this product named, inside a directory of its
// own name. It is the second gate over the push direction, and it is asked
// before any host path can reach an argv token.
func ValidateTransferHostPath(path string) error {
	if path == "" || len(path) > maxTransferHostPathLength || !transferHostPathPattern.MatchString(path) {
		return ErrTransferHostPathInvalid
	}
	if strings.Contains(path, "..") {
		return ErrTransferHostPathInvalid
	}
	return validateArgToken(path)
}

// ValidateImeComponent reports whether component is an input-method component
// name: a dotted package, a separator, and the service half. It is a NAME, not
// command text, which is what lets the write be admitted as a single bounded
// argument rather than as free text.
func ValidateImeComponent(component string) error {
	if component == "" || len(component) > maxImeComponentLength || !imeComponentPattern.MatchString(component) {
		return ErrImeComponentInvalid
	}
	return validateArgToken(component)
}

// --- argv builders -------------------------------------------------------
//
// Every builder returns a fresh slice of fixed tokens. No builder accepts free
// text, and no builder concatenates a caller value into an existing token.

func devicesArgv() []string { return []string{"devices", "-l"} }

func versionArgv() []string { return []string{"version"} }

func getStateArgv() []string { return []string{"get-state"} }

func screencapArgv() []string { return []string{"exec-out", "screencap", "-p"} }

func deviceNameArgv() []string { return []string{"shell", "settings", "get", "global", "device_name"} }

// reportedSerialProperty is the Android build property that carries a device's
// own serial. It is the one property admitted to identify a DEVICE rather than
// to describe a transport: adb names a TCP device by the address it answers on,
// so that serial changes with the device's address, while this one stays with
// the device and lets the registry keep one identity across a move.
const reportedSerialProperty = "ro.serialno"

// reportedSerialArgv reads the serial the device reports about itself. Every
// token is fixed: the property is a constant rather than a caller value, so the
// array has no variable position at all and cannot express command text.
func reportedSerialArgv() []string { return []string{"shell", "getprop", reportedSerialProperty} }

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
//
// The five typed device inputs are the one admission beyond the read-only
// builders (ADR-0010). It is narrow by construction: every admitted array
// reaches a fixed device binary through adb's own remote-shell transport, and
// every variable token is either a bounded decimal integer or a name that has
// matched a strict allow-list pattern. No admitted position accepts whitespace,
// a quote, a shell metacharacter, a path, a flag, a separator, a redirect, a
// substitution or a second command, so no admitted array can express command
// text.
//
// The render-size declaration read (`shell wm size`) is the one admission
// beside those input arrays (ARC-75, recorded as an amendment to ADR-0010). It
// is a read-only precondition read the render-space cross-check cannot obtain a
// device reading without, so it is listed with the read-only builders rather
// than with the typed inputs, and it is spelled out here in literal tokens
// rather than imported from the builder that emits it.
//
// The catalogued device settings are the third admission (ARC-137). Each is a
// fixed argument array with zero variable positions — every token is a literal
// spelled out below, not imported from the builder that emits it — so there is
// nothing to parameterise and no bound to derive. They are CATALOGUED general
// commands in the sense of AGENTS.md section 3: named operations with a bounded
// (here empty) parameter set, dispatched as an argv array without a shell.
//
// The three admissions are recognisers of their own — `matchesDeviceInputAllowlist`,
// `matchesDeviceSettingsAllowlist` and `matchesReadOnlyAllowlist` — and they are
// asked in that order. That order is not load-bearing: the three are provably
// disjoint, so no array is admitted by more than one, and
// `allowlist_classification_test.go` asserts that over every admitted array,
// every near miss and a token-mutation cross-product. The order is pinned
// anyway, so a future admission that made them overlap fails a test instead of
// silently changing an array's classification.
//
// The live mirror's shapes are the fifth admission (ARC-143): the server push,
// the two reverse-tunnel forms and the device-side server launch, each
// constructed by one of the builders in `mirror.go`. They are device-scoped and
// serial-free like every other shape here — the serial is supplied by the entry
// point as its own -s token — and they are admitted in a recogniser of their own
// for the same reason as the others: their separation from the rest is a safety
// property, so it has to be askable by a test rather than held in a comment.
func matchesAllowlist(args []string) (string, bool) {
	if name, ok := matchesDeviceInputAllowlist(args); ok {
		return name, true
	}
	if name, ok := matchesDeviceSettingsAllowlist(args); ok {
		return name, true
	}
	if name, ok := matchesDeviceOperationAllowlist(args); ok {
		return name, true
	}
	if name, ok := matchesReadOnlyAllowlist(args); ok {
		return name, true
	}
	if name, ok := matchesDiagnosticsAllowlist(args); ok {
		return name, true
	}
	if name, ok := matchesTransportAllowlist(args); ok {
		return name, true
	}
	if name, ok := matchesMirrorAllowlist(args); ok {
		return name, true
	}
	return matchesHostAllowlist(args)
}

// matchesDeviceOperationAllowlist recognises exactly the argument arrays the
// catalogued device operations of the big-frame control panel use: the reboot,
// the three halves of a keyboard switch, and the five shapes a file or package
// operation composes.
//
// It is a recogniser of its own for the same reason as every other one: the
// separation between "a fixed builder shape this product admits" and
// "everything else" is a safety property, and a property can only be asserted
// if it can be asked directly. Every case below spells its tokens out
// literally rather than importing the builder that emits them, so this gate is
// an independent second reading of the same shapes.
//
// The variable positions are bounded and NAMED, never free text:
//
//   - a file NAME with no separator of any kind, which this adapter itself
//     composes into the one device directory this product owns;
//   - a host transfer path whose own file name this product generated;
//   - a package name and an input-method component, both dotted identifiers
//     with no whitespace, no flag, no path and no second command.
//
// No position accepts whitespace, a quote, a shell metacharacter, a redirection,
// a substitution or a second command, so no admitted array here can express
// command text.
func matchesDeviceOperationAllowlist(args []string) (string, bool) {
	switch {
	case equalArgv(args, []string{"reboot"}):
		return "device-reboot", true
	case equalArgv(args, []string{"shell", "ime", "list", "-s"}):
		return "keyboard-enabled-list", true
	case len(args) == 4 && args[0] == "shell" && args[1] == "ime" && args[2] == "set" && ValidateImeComponent(args[3]) == nil:
		return "keyboard-set", true
	case equalArgv(args, []string{"shell", "settings", "get", "secure", "default_input_method"}):
		return "keyboard-default-read", true
	case len(args) == 5 && args[0] == "shell" && args[1] == "stat" && args[2] == "-c" && args[3] == "%s" && ValidateDeviceInboxPath(args[4]) == nil:
		return "device-file-size", true
	case len(args) == 3 && args[0] == "push" && ValidateTransferHostPath(args[1]) == nil && ValidateDeviceInboxPath(args[2]) == nil:
		return "device-file-push", true
	case len(args) == 3 && args[0] == "exec-out" && args[1] == "cat" && ValidateDeviceInboxPath(args[2]) == nil:
		return "device-file-read", true
	case len(args) == 6 && args[0] == "shell" && args[1] == "pm" && args[2] == "install" && args[3] == "-r" && args[4] == "-t" && ValidateDeviceInboxPath(args[5]) == nil:
		return "package-install", true
	case len(args) == 4 && args[0] == "shell" && args[1] == "pm" && args[2] == "path" && validatePackageName(args[3]) == nil:
		return "package-path-read", true
	default:
		return "", false
	}
}

// matchesDeviceSettingsAllowlist recognises exactly the ten argument arrays the
// catalogued device settings use: the five writes that change rotation lock and
// autofill, the four reads each operation uses to verify its own postcondition,
// and the fixed read of Android's global device_name setting.
//
// Every array is arity-fixed and literal. There is no decimal, no name, no flag,
// no path and no second command in any position, so this admission cannot
// express command text even in principle; the reads are listed with the writes
// because both are this family's own shapes, and the read operation names are
// not action-catalog kinds, so no read can be selected as an operator action.
func matchesDeviceSettingsAllowlist(args []string) (string, bool) {
	switch {
	// Rotation lock, the two writes: auto-rotate off, then the device held in
	// its natural orientation. Both are required; either alone leaves the
	// device presenting at an orientation the recording was not taken in.
	case equalArgv(args, []string{"shell", "settings", "put", "system", "accelerometer_rotation", "0"}):
		return "settings-rotation-auto-off", true
	case equalArgv(args, []string{"shell", "settings", "put", "system", "user_rotation", "0"}):
		return "settings-rotation-zero", true
	// Autofill off, the three writes: remove the selected service, disable the
	// augmented service for user 0, then reset the autofill manager so the
	// change takes effect. The order is load-bearing and is the order the
	// primitive issues them in.
	case equalArgv(args, []string{"shell", "settings", "delete", "secure", "autofill_service"}):
		return "settings-autofill-service-delete", true
	case equalArgv(args, []string{"shell", "cmd", "autofill", "set", "default-augmented-service-enabled", "0", "false"}):
		return "settings-autofill-augmented-off", true
	case equalArgv(args, []string{"shell", "cmd", "autofill", "reset"}):
		return "settings-autofill-reset", true
	// The read-backs. Each is the read half of the setting the write above it
	// changes, and it is what makes "applied" a fact read off the device rather
	// than a report that a command exited zero.
	case equalArgv(args, []string{"shell", "settings", "get", "system", "accelerometer_rotation"}):
		return "settings-rotation-auto-read", true
	case equalArgv(args, []string{"shell", "settings", "get", "system", "user_rotation"}):
		return "settings-rotation-zero-read", true
	case equalArgv(args, []string{"shell", "settings", "get", "secure", "autofill_service"}):
		return "settings-autofill-service-read", true
	case equalArgv(args, []string{"shell", "cmd", "autofill", "get", "default-augmented-service-enabled"}):
		return "settings-autofill-augmented-read", true
	case equalArgv(args, deviceNameArgv()):
		return "settings-device-name", true
	default:
		return "", false
	}
}

// matchesReadOnlyAllowlist recognises the read-only builders this adapter issues
// and the render-size declaration read admitted for the render-space
// cross-check. It is deliberately a function of its own rather than an inline
// switch, because the separation between admissions is a safety property
// and a property can only be asserted if both sides of it can be asked
// independently in a test.
func matchesReadOnlyAllowlist(args []string) (string, bool) {
	switch {
	// The render-size declaration read. It is the narrowest admission this
	// adapter has: arity three, three fixed literals, and zero variable
	// positions — no caller value, no decimal, no name, no flag, no path and no
	// second command, so there is nothing to parameterise and no bound to
	// derive. Every near miss (a second token, another subcommand, a shell, a
	// case variant, either token named by path) is a different array and stays
	// refused.
	case len(args) == 3 && args[0] == "shell" && args[1] == "wm" && args[2] == "size":
		return "wm-size", true
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

// matchesDeviceInputAllowlist recognises exactly the six argument arrays the
// five typed device input builders produce: one tap, one swipe, one key event,
// one typed text and the two app launch shapes (a package-only launch and a
// launch that names one activity component).
func matchesDeviceInputAllowlist(args []string) (string, bool) {
	switch {
	case len(args) == 5 && args[0] == "shell" && args[1] == "input" && args[2] == "tap" &&
		isBoundedDecimal(args[3], maxInputCoordinate) && isBoundedDecimal(args[4], maxInputCoordinate):
		return "input-tap", true
	case len(args) == 8 && args[0] == "shell" && args[1] == "input" && args[2] == "swipe" &&
		isBoundedDecimal(args[3], maxInputCoordinate) && isBoundedDecimal(args[4], maxInputCoordinate) &&
		isBoundedDecimal(args[5], maxInputCoordinate) && isBoundedDecimal(args[6], maxInputCoordinate) &&
		isBoundedDecimalBetween(args[7], 1, maxInputSwipeDurationMillis):
		return "input-swipe", true
	case len(args) == 4 && args[0] == "shell" && args[1] == "input" && args[2] == "keyevent" &&
		isBoundedDecimalBetween(args[3], 1, maxInputKeyCode):
		return "input-keyevent", true
	case len(args) == 4 && args[0] == "shell" && args[1] == "input" && args[2] == "text" && isInputTextToken(args[3]):
		return "input-text", true
	case len(args) == 7 && args[0] == "shell" && args[1] == "monkey" && args[2] == "-p" &&
		validatePackageName(args[3]) == nil && args[4] == "-c" && args[5] == launcherCategory && args[6] == launcherCountToken:
		return "launch-app-package", true
	case len(args) == 5 && args[0] == "shell" && args[1] == "am" && args[2] == "start" && args[3] == "-n" &&
		validateLaunchComponent(args[4]) == nil:
		return "launch-app-activity", true
	default:
		return "", false
	}
}

// isBoundedDecimal reports a canonical unsigned decimal token within [0, max]:
// digits only, no sign, no whitespace, no leading zero, and inside the bound.
// Canonical form matters because it is the only form a builder emits.
func isBoundedDecimal(token string, max uint64) bool {
	return isBoundedDecimalBetween(token, 0, max)
}

func isBoundedDecimalBetween(token string, min, max uint64) bool {
	if token == "" || len(token) > 20 {
		return false
	}
	if len(token) > 1 && token[0] == '0' {
		return false
	}
	for index := 0; index < len(token); index++ {
		if token[index] < '0' || token[index] > '9' {
			return false
		}
	}
	value, err := strconv.ParseUint(token, 10, 64)
	if err != nil {
		return false
	}
	return value >= min && value <= max
}

// isInputTextToken reports a token the typed-text builder could have emitted:
// one argument-safe token in which the device input command's own space escape
// (`%s`) is the only place a percent sign appears. A literal percent would be
// typed as something other than itself, so it is refused rather than escaped.
func isInputTextToken(token string) bool {
	if token == "" || len(token) > maxArgTokenLength {
		return false
	}
	if err := validateArgToken(token); err != nil {
		return false
	}
	for index := 0; index < len(token); index++ {
		if token[index] != '%' {
			continue
		}
		if index+1 >= len(token) || token[index+1] != 's' {
			return false
		}
		index++
	}
	return true
}

// validatePackageName accepts a dotted package name and nothing else.
func validatePackageName(name string) error {
	if name == "" || len(name) > maxComponentLength || !packageNamePattern.MatchString(name) {
		return fmt.Errorf("%w: package name is not allow-listed", ErrArgvNotAllowlisted)
	}
	return nil
}

// validateLaunchComponent accepts the `<package>/<activity>` component token an
// app launch composes from two separately allow-listed names. The package and
// the activity are validated apart, so a token cannot smuggle a path, a
// traversal, a second command or a flag through either half.
func validateLaunchComponent(token string) error {
	if len(token) > 2*maxComponentLength {
		return fmt.Errorf("%w: launch component is over-long", ErrArgvNotAllowlisted)
	}
	name, activity, ok := strings.Cut(token, "/")
	if !ok || activity == "" {
		return fmt.Errorf("%w: launch component is not a package/activity pair", ErrArgvNotAllowlisted)
	}
	if err := validatePackageName(name); err != nil {
		return err
	}
	if activity == "" || len(activity) > maxComponentLength || !componentNamePattern.MatchString(activity) ||
		!strings.Contains(activity, ".") || strings.Contains(activity, "..") {
		return fmt.Errorf("%w: launch activity is not allow-listed", ErrArgvNotAllowlisted)
	}
	return nil
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
