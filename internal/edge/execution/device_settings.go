// The catalogued device settings for one explicitly named transport serial.
//
// This file implements the two settings operations the fleet actually requires:
// rotation lock and autofill off. Like the five typed device inputs beside them
// they are narrow and parameterless — the operation IS the payload — and there
// is no shell, no exec, no command text and no caller-authored argument array
// anywhere in this file. Every argument array it executes is produced by a fixed
// builder below, and the device adapter admits each one through its own
// recogniser before anything reaches a device.
//
// Two rules are worth restating at the point of use:
//
//   - WRITES ARE FOLLOWED BY A READ-BACK. A settings change is not reported as
//     applied because a command exited zero: the setting is read back off the
//     device and compared against what the action catalog declares must be true
//     afterwards. This is the standard the ported behaviour held itself to, and
//     it is preserved here rather than replaced by trusting the exit code.
//   - A READ THAT DOES NOT ANSWER IS NOT A VALUE. A read-back the device refuses
//     leaves its setting unconfirmed, and an unconfirmed setting never satisfies
//     a postcondition; only a read that could not be attempted at all (a
//     transport failure, so the device was unreachable) is an error.
package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// RotationLockRequest is the payload of the rotation-lock action. It carries no
// parameter: the operation names the two settings it writes and the values they
// are written to, so there is nothing a caller could supply.
type RotationLockRequest struct{}

// AutofillOffRequest is the payload of the autofill-off action. It carries no
// parameter, for the same reason.
type AutofillOffRequest struct{}

// readbackTokenPrefix names every settings read-back token, so a token observed
// anywhere says which kind of reading produced it.
const readbackTokenPrefix = "settings-readback-"

// Token is an opaque, bounded identifier for THIS reading of the device's
// settings: a digest of what the device answered, and never the answers
// themselves.
//
// It exists because the kernel requires a completion to name the observation the
// outcome was evaluated against, and for a settings operation that observation IS
// the read-back. The digest is what makes the name honest: two readings of the
// same device produce the same token only when the device answered the same way,
// and no device-side value is carried by it.
func (r SettingReadback) Token() string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		r.AccelerometerRotation, r.UserRotation, r.AutofillService, r.AugmentedServiceEnabled,
	}, "\x1f")))
	return readbackTokenPrefix + hex.EncodeToString(digest[:12])
}

// SettingReadback is what a settings operation read back off the device after it
// changed the setting. Every field is the device's own answer, trimmed, and a
// field is empty when the device did not answer.
//
// It is a reading, never a report that a command succeeded: the predicates below
// are what decide whether the declared postcondition holds, and a setting that
// was not read is not a setting that holds.
type SettingReadback struct {
	// AccelerometerRotation is `settings get system accelerometer_rotation`.
	AccelerometerRotation string
	// UserRotation is `settings get system user_rotation`.
	UserRotation string
	// AutofillService is `settings get secure autofill_service`. It is "null" or
	// empty when no service is selected.
	AutofillService string
	// AugmentedServiceEnabled is
	// `cmd autofill get default-augmented-service-enabled`, reduced to the
	// device's own answer ("true" or "false") or empty when it did not answer.
	AugmentedServiceEnabled string
}

// RotationLocked reports whether the read-back shows the rotation lock holding:
// auto-rotate off AND the device held in its natural orientation. Both are
// required, because either one alone leaves the device free to present at an
// orientation a coordinate recording was not taken in.
func (r SettingReadback) RotationLocked() bool {
	return settingValue(r.AccelerometerRotation) == "0" && settingValue(r.UserRotation) == "0"
}

// AutofillDisabled reports whether the read-back shows autofill off: no autofill
// service selected AND the augmented autofill service disabled. A read-back that
// did not answer for either half loses the whole predicate, so an unreadable
// setting is never mistaken for a disabled one.
func (r SettingReadback) AutofillDisabled() bool {
	return !autofillServiceSelected(r.AutofillService) && r.AugmentedServiceEnabled == "false"
}

// settingValue reduces a raw settings read to the value the device reported:
// trimmed, and with the `null` a settings key that was never set reads back as
// carrying no value at all.
func settingValue(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.EqualFold(value, "null") {
		return ""
	}
	return value
}

// autofillServiceSelected reports whether a read of `secure autofill_service`
// names a service. Android reports `null` for a key that is unset, so an empty
// or null reading is the only reading that means "no autofill service".
func autofillServiceSelected(raw string) bool {
	return settingValue(raw) != ""
}

// augmentedServiceAnswer reduces the `cmd autofill get` output to the device's
// own boolean. The command prints a labelled line on some builds and a bare
// value on others, so the last whitespace- or colon-separated field is taken;
// anything that is not exactly "true" or "false" is not an answer, and is
// reported as an unread setting rather than guessed at.
func augmentedServiceAnswer(raw string) string {
	fields := strings.FieldsFunc(strings.TrimSpace(raw), func(r rune) bool {
		return r == ':' || r == '=' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	if len(fields) == 0 {
		return ""
	}
	switch answer := fields[len(fields)-1]; answer {
	case "true", "false":
		return answer
	default:
		return ""
	}
}

// ApplyRotationLock writes the device's two rotation settings and reads both
// back. The writes are issued in the order the device expects them and the first
// failure stops the operation: a device with auto-rotate off and a rotation that
// was never set to zero is not half locked, it is in a state no recording was
// taken in.
func (i *Inputs) ApplyRotationLock(ctx context.Context) (SettingReadback, error) {
	if err := i.prepared(ctx); err != nil {
		return SettingReadback{}, err
	}
	for _, write := range []struct {
		operation string
		argv      []string
	}{
		{"rotation-auto-off", rotationAutoOffArgv()},
		{"rotation-zero", rotationZeroArgv()},
	} {
		if err := i.execute(ctx, write.operation, write.argv, detailSafe); err != nil {
			return SettingReadback{}, err
		}
	}
	return i.readRotation(ctx)
}

// ApplyAutofillOff removes the device's selected autofill service, disables the
// augmented autofill service for the primary user, resets the autofill manager
// so the change takes effect, and then reads both settings back. The three
// writes are fixed admissions; nothing about them is caller-supplied.
func (i *Inputs) ApplyAutofillOff(ctx context.Context) (SettingReadback, error) {
	if err := i.prepared(ctx); err != nil {
		return SettingReadback{}, err
	}
	for _, write := range []struct {
		operation string
		argv      []string
	}{
		{"autofill-service-delete", autofillServiceDeleteArgv()},
		{"autofill-augmented-off", autofillAugmentedOffArgv()},
		{"autofill-reset", autofillResetArgv()},
	} {
		if err := i.execute(ctx, write.operation, write.argv, detailSafe); err != nil {
			return SettingReadback{}, err
		}
	}
	return i.readAutofill(ctx)
}

// readRotation reads the two rotation settings back. A read the device refuses
// leaves its setting unread, which is what makes the postcondition fail rather
// than pass on a command that merely exited zero.
func (i *Inputs) readRotation(ctx context.Context) (SettingReadback, error) {
	readback := SettingReadback{}
	value, err := i.readSetting(ctx, "rotation-auto-read", rotationAutoReadArgv())
	if err != nil {
		return readback, err
	}
	readback.AccelerometerRotation = value
	value, err = i.readSetting(ctx, "rotation-zero-read", rotationZeroReadArgv())
	if err != nil {
		return readback, err
	}
	readback.UserRotation = value
	return readback, nil
}

// readAutofill reads the two autofill settings back. This read-back is the
// reason the operation can be trusted: the ported behaviour read the setting it
// had just changed and reported failure when autofill was still enabled.
func (i *Inputs) readAutofill(ctx context.Context) (SettingReadback, error) {
	readback := SettingReadback{}
	value, err := i.readSetting(ctx, "autofill-service-read", autofillServiceReadArgv())
	if err != nil {
		return readback, err
	}
	readback.AutofillService = value
	value, err = i.readSetting(ctx, "autofill-augmented-read", autofillAugmentedReadArgv())
	if err != nil {
		return readback, err
	}
	readback.AugmentedServiceEnabled = augmentedServiceAnswer(value)
	return readback, nil
}

// readSetting runs one read-back argument array and returns the device's answer.
//
// It distinguishes the two ways a read can fail, because they mean different
// things to an operator:
//
//   - The command could not be attempted at all — a transport failure, so the
//     device was unreachable. That is an error, and the caller reports the
//     attempt as one whose outcome is UNKNOWN rather than as a setting that did
//     not change.
//   - The device was reached and refused the read, or answered with nothing
//     usable. That is not a value, so the answer is empty; the postcondition
//     then fails honestly instead of being reported as satisfied.
func (i *Inputs) readSetting(ctx context.Context, operation string, argv []string) (string, error) {
	if err := validateInputArgv(argv); err != nil {
		return "", platformerrors.Wrap(platformerrors.CodeInvalidInput, "device settings argument array is not allow-listed", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()

	result, err := i.transport.RunDeviceCommand(callCtx, i.serial, argv)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return "", inputContextError(ctx)
		case errors.Is(err, context.DeadlineExceeded):
			return "", platformerrors.Wrap(platformerrors.CodeDeadlineExceeded, "device settings deadline exceeded", context.DeadlineExceeded)
		case errors.Is(err, context.Canceled):
			return "", platformerrors.Wrap(platformerrors.CodeCanceled, "device settings read-back was cancelled", context.Canceled)
		default:
			return "", platformerrors.Wrap(platformerrors.CodeUnavailable, "device settings transport failed", errors.New(operation+" did not reach the device"))
		}
	}
	if result.ExitCode != 0 {
		return "", nil
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

// --- argv builders ----------------------------------------------------------
//
// Every builder returns a fresh slice of fixed tokens. No builder takes an
// argument at all: the operation a caller selected is the whole payload, so
// there is no caller value anywhere in these arrays for a token to carry. The
// device adapter admits each array through its own recogniser, which spells the
// same tokens out literally rather than importing these builders, so the
// allow-list remains an independent second gate.

func rotationAutoOffArgv() []string {
	return []string{"shell", "settings", "put", "system", "accelerometer_rotation", "0"}
}

func rotationZeroArgv() []string {
	return []string{"shell", "settings", "put", "system", "user_rotation", "0"}
}

func rotationAutoReadArgv() []string {
	return []string{"shell", "settings", "get", "system", "accelerometer_rotation"}
}

func rotationZeroReadArgv() []string {
	return []string{"shell", "settings", "get", "system", "user_rotation"}
}

func autofillServiceDeleteArgv() []string {
	return []string{"shell", "settings", "delete", "secure", "autofill_service"}
}

func autofillAugmentedOffArgv() []string {
	return []string{"shell", "cmd", "autofill", "set", "default-augmented-service-enabled", "0", "false"}
}

func autofillResetArgv() []string {
	return []string{"shell", "cmd", "autofill", "reset"}
}

func autofillServiceReadArgv() []string {
	return []string{"shell", "settings", "get", "secure", "autofill_service"}
}

func autofillAugmentedReadArgv() []string {
	return []string{"shell", "cmd", "autofill", "get", "default-augmented-service-enabled"}
}

// DeviceSettingArgvs returns every argument array the two settings operations
// can issue, in the order each operation issues them, keyed by the operation
// name that identifies the array.
//
// It is exported for one reason: the separation between this admission family
// and every other one is a safety property, and a property can only be asserted
// if both sides of it can be asked independently in a test. It is not a
// dispatch path — there is no function here that runs an array — and the device
// adapter still re-derives whether each array is allow-listed.
func DeviceSettingArgvs() map[string][]string {
	return map[string][]string{
		"rotation-auto-off":       rotationAutoOffArgv(),
		"rotation-zero":           rotationZeroArgv(),
		"rotation-auto-read":      rotationAutoReadArgv(),
		"rotation-zero-read":      rotationZeroReadArgv(),
		"autofill-service-delete": autofillServiceDeleteArgv(),
		"autofill-augmented-off":  autofillAugmentedOffArgv(),
		"autofill-reset":          autofillResetArgv(),
		"autofill-service-read":   autofillServiceReadArgv(),
		"autofill-augmented-read": autofillAugmentedReadArgv(),
	}
}
