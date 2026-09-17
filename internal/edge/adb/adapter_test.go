package adb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/platform/clock"
	"drift.local/drift-next/internal/platform/ids"
)

const (
	testExecutable = "/opt/android/platform-tools/adb"
	testSerial     = "R5CT30ABCD"
)

var testCapturedAt = time.Date(2026, time.September, 15, 4, 30, 0, 0, time.UTC)

func newTestAdapter(t *testing.T, runner Runner, opts ...Option) *Adapter {
	t.Helper()
	options := append([]Option{
		WithClock(clock.NewFixed(testCapturedAt)),
		WithIDGenerator(ids.NewSequence("corr-1", "corr-2", "corr-3")),
	}, opts...)
	adapter, err := NewAdapter(testExecutable, runner, options...)
	if err != nil {
		t.Fatalf("NewAdapter() = %v", err)
	}
	return adapter
}

func deviceArgs(args ...string) []string {
	return append([]string{"-s", testSerial}, args...)
}

func TestNewAdapterRequiresExplicitConfiguration(t *testing.T) {
	runner := NewFakeRunner()

	if _, err := NewAdapter("", runner); !errors.Is(err, ErrExecutableRequired) {
		t.Fatalf("NewAdapter(\"\") = %v, want ErrExecutableRequired", err)
	}
	if _, err := NewAdapter("adb", runner); !errors.Is(err, ErrExecutableRequired) {
		t.Fatalf("NewAdapter(relative) = %v, want ErrExecutableRequired", err)
	}
	if _, err := NewAdapter(testExecutable, nil); !errors.Is(err, ErrRunnerRequired) {
		t.Fatalf("NewAdapter(nil runner) = %v, want ErrRunnerRequired", err)
	}
	adapter, err := NewAdapter(testExecutable, runner)
	if err != nil {
		t.Fatalf("NewAdapter() = %v", err)
	}
	if adapter.Version() != AdapterVersion {
		t.Fatalf("Version() = %q, want %q", adapter.Version(), AdapterVersion)
	}
}

const devicesFixture = `List of devices attached
* daemon started successfully *
emulator-5554          device product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64a transport_id:1
R5CT30ABCD             device usb:1-2 product:b0q model:SM_S908B device:b0q transport_id:2
ZY223UNAUTH            unauthorized usb:1-3 transport_id:3
192.168.1.10:5555      offline transport_id:4
0123456789ABCDEF       no permissions (user is not in the plugdev group); see [http://example.invalid/adb]
`

func TestEnumerateParsesEveryTransportState(t *testing.T) {
	runner := NewFakeRunner().RespondStdout(devicesArgv(), devicesFixture)
	adapter := newTestAdapter(t, runner)

	devices, err := adapter.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate() = %v", err)
	}
	if len(devices) != 5 {
		t.Fatalf("len(devices) = %d, want 5", len(devices))
	}

	want := []DiscoveredDevice{
		{Serial: "emulator-5554", State: StateDevice, Product: "sdk_gphone64_arm64", Model: "sdk_gphone64_arm64", Device: "emu64a", TransportID: "1", ConnectionType: ConnectionUSB},
		{Serial: "R5CT30ABCD", State: StateDevice, Product: "b0q", Model: "SM_S908B", Device: "b0q", TransportID: "2", ConnectionType: ConnectionUSB},
		{Serial: "ZY223UNAUTH", State: StateUnauthorized, TransportID: "3", ConnectionType: ConnectionUSB},
		{Serial: "192.168.1.10:5555", State: StateOffline, TransportID: "4", ConnectionType: ConnectionTCP},
		{Serial: "0123456789ABCDEF", State: StateNoPermissions, ConnectionType: ConnectionUSB},
	}
	for index, expected := range want {
		if devices[index] != expected {
			t.Fatalf("devices[%d] = %+v, want %+v", index, devices[index], expected)
		}
	}

	invocations := runner.Invocations()
	if len(invocations) != 3 {
		t.Fatalf("len(invocations) = %d, want enumeration plus one name read per usable transport", len(invocations))
	}
	if invocations[0].Executable != testExecutable {
		t.Fatalf("executable = %q, want %q", invocations[0].Executable, testExecutable)
	}
	if strings.Join(invocations[0].Args, " ") != "devices -l" {
		t.Fatalf("args = %q, want [devices -l]", invocations[0].Args)
	}
}

func TestEnumerateCapturesGlobalAndroidDeviceNameForUsableTransport(t *testing.T) {
	runner := NewFakeRunner().
		RespondStdout(devicesArgv(), "List of devices attached\nR5CT30ABCD             device usb:1-2 product:b0q model:SM_G9750 device:b0q transport_id:2\n").
		RespondStdout(append([]string{"-s", testSerial}, deviceNameArgv()...), "ALTA 1\n")
	adapter := newTestAdapter(t, runner)

	devices, err := adapter.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate() = %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("len(devices) = %d, want one", len(devices))
	}
	if devices[0].DeviceName != "ALTA 1" {
		t.Fatalf("DeviceName = %q, want the Android global device_name", devices[0].DeviceName)
	}
	invocations := runner.Invocations()
	if len(invocations) != 2 {
		t.Fatalf("len(invocations) = %d, want enumeration plus one name read", len(invocations))
	}
	if got := strings.Join(invocations[1].Args, " "); got != "-s "+testSerial+" shell settings get global device_name" {
		t.Fatalf("name read args = %q, want the fixed device_name argv", got)
	}
}

func TestEnumerateHandlesAnEmptyDeviceList(t *testing.T) {
	runner := NewFakeRunner().RespondStdout(devicesArgv(), "List of devices attached\n\n")
	adapter := newTestAdapter(t, runner)

	devices, err := adapter.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate() = %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("len(devices) = %d, want 0", len(devices))
	}
}

func TestEnumerateRejectsUnsafeSerialsInOutput(t *testing.T) {
	runner := NewFakeRunner().RespondStdout(devicesArgv(),
		"List of devices attached\nR5CT30;id            device transport_id:2\n")
	adapter := newTestAdapter(t, runner)

	_, err := adapter.Enumerate(context.Background())
	if FailureClassOf(err) != domain.FailureObservation {
		t.Fatalf("Enumerate() failure class = %q, want observation_error (err=%v)", FailureClassOf(err), err)
	}
}

func TestValidateDeviceClassifiesUnusableTransports(t *testing.T) {
	runner := NewFakeRunner().RespondStdout(devicesArgv(), devicesFixture)
	adapter := newTestAdapter(t, runner)

	tests := []struct {
		name   string
		serial string
		want   domain.FailureClass
	}{
		{name: "unauthorized", serial: "ZY223UNAUTH", want: domain.FailureTransport},
		{name: "offline", serial: "192.168.1.10:5555", want: domain.FailureDeviceOffline},
		{name: "no permissions", serial: "0123456789ABCDEF", want: domain.FailureTransport},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			device, err := adapter.ValidateDevice(context.Background(), test.serial)
			if FailureClassOf(err) != test.want {
				t.Fatalf("failure class = %q, want %q", FailureClassOf(err), test.want)
			}
			if device.Serial != test.serial {
				t.Fatalf("device.Serial = %q, want %q; the observed state must stay visible", device.Serial, test.serial)
			}
		})
	}

	device, err := adapter.ValidateDevice(context.Background(), testSerial)
	if err != nil {
		t.Fatalf("ValidateDevice(usable) = %v", err)
	}
	if device.TransportID != "2" {
		t.Fatalf("TransportID = %q, want 2", device.TransportID)
	}
}

func TestValidateDeviceRejectsMissingAndUnsafeSerials(t *testing.T) {
	runner := NewFakeRunner().RespondStdout(devicesArgv(), devicesFixture)
	adapter := newTestAdapter(t, runner)

	if _, err := adapter.ValidateDevice(context.Background(), "NOTATTACHED"); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("ValidateDevice(missing) = %v, want ErrDeviceNotFound", err)
	}
	_, err := adapter.ValidateDevice(context.Background(), "R5CT30;id")
	if !errors.Is(err, ErrSerialInvalid) {
		t.Fatalf("ValidateDevice(unsafe) = %v, want ErrSerialInvalid", err)
	}
	for _, invocation := range runner.Invocations() {
		for _, arg := range invocation.Args {
			if strings.Contains(arg, ";") {
				t.Fatalf("an unsafe serial reached the runner: %q", invocation.Args)
			}
		}
	}
}

func TestEveryDeviceOperationValidatesTheSerialFirst(t *testing.T) {
	runner := NewFakeRunner().RespondDefault(FakeResponse{})
	adapter := newTestAdapter(t, runner)
	ctx := context.Background()

	operations := map[string]func(string) error{
		"validate-device": func(serial string) error { _, err := adapter.ValidateDevice(ctx, serial); return err },
		"health":          func(serial string) error { _, err := adapter.Health(ctx, serial); return err },
		"transport":       func(serial string) error { _, err := adapter.TransportIdentity(ctx, serial); return err },
		"screenshot":      func(serial string) error { _, err := adapter.Screenshot(ctx, serial); return err },
		"reattach":        func(serial string) error { _, err := adapter.ReattachReadOnly(ctx, serial, "1"); return err },
		"allowlisted":     func(serial string) error { _, err := adapter.RunAllowlisted(ctx, serial, getStateArgv()); return err },
	}
	unsafe := []string{"", "   ", "a;id", "a|id", "a$(id)", "a`id`", "a\nb"}

	for name, operation := range operations {
		for _, serial := range unsafe {
			err := operation(serial)
			if !errors.Is(err, ErrSerialInvalid) && !errors.Is(err, ErrSerialRequired) {
				t.Fatalf("%s(%q) = %v, want a serial validation failure", name, serial, err)
			}
		}
	}
	if count := len(runner.Invocations()); count != 0 {
		t.Fatalf("runner was invoked %d times for rejected serials, want 0", count)
	}
}

func TestHealthReportsAnObservableButUnusableDevice(t *testing.T) {
	runner := NewFakeRunner().
		Respond(deviceArgs(getStateArgv()...), FakeResponse{Result: Result{Stdout: []byte("offline\n"), Duration: 12 * time.Millisecond}})
	adapter := newTestAdapter(t, runner)

	report, err := adapter.Health(context.Background(), testSerial)
	if err != nil {
		t.Fatalf("Health() = %v, want nil for an observable device", err)
	}
	if report.State != "offline" || report.FailureClass != domain.FailureDeviceOffline {
		t.Fatalf("report = %+v, want an offline classification", report)
	}
	if report.Healthy() {
		t.Fatal("Healthy() = true for an offline device")
	}
	if report.Latency != 12*time.Millisecond {
		t.Fatalf("Latency = %s, want 12ms", report.Latency)
	}
}

func TestHealthCollectsAllowlistedProperties(t *testing.T) {
	release, err := getPropArgv("ro.build.version.release")
	if err != nil {
		t.Fatalf("getPropArgv() = %v", err)
	}
	sdk, err := getPropArgv("ro.build.version.sdk")
	if err != nil {
		t.Fatalf("getPropArgv() = %v", err)
	}
	model, err := getPropArgv("ro.product.model")
	if err != nil {
		t.Fatalf("getPropArgv() = %v", err)
	}

	runner := NewFakeRunner().
		Respond(deviceArgs(getStateArgv()...), FakeResponse{Result: Result{Stdout: []byte("device\n"), Duration: 5 * time.Millisecond}}).
		RespondStdout(devicesArgv(), devicesFixture).
		Respond(deviceArgs(release...), FakeResponse{Result: Result{Stdout: []byte("14\n"), Duration: 2 * time.Millisecond}}).
		Respond(deviceArgs(sdk...), FakeResponse{Result: Result{Stdout: []byte("34\n"), Duration: 2 * time.Millisecond}}).
		Respond(deviceArgs(model...), FakeResponse{Result: Result{Stdout: []byte("SM-S908B\n"), Duration: 2 * time.Millisecond}}).
		RespondStdout(versionArgv(), "Android Debug Bridge version 1.0.41\nVersion 35.0.2-12345678\n")
	adapter := newTestAdapter(t, runner)

	report, err := adapter.Health(context.Background(), testSerial)
	if err != nil {
		t.Fatalf("Health() = %v", err)
	}
	if !report.Healthy() {
		t.Fatalf("report = %+v, want a healthy report", report)
	}
	if report.AndroidVersion != "14" || report.APILevel != "34" || report.Model != "SM-S908B" {
		t.Fatalf("report = %+v, want the allowlisted property values", report)
	}
	if report.TransportID != "2" {
		t.Fatalf("TransportID = %q, want 2", report.TransportID)
	}
	if report.PlatformToolsVersion != "35.0.2-12345678" {
		t.Fatalf("PlatformToolsVersion = %q, want 35.0.2-12345678", report.PlatformToolsVersion)
	}
	if report.Latency != 11*time.Millisecond {
		t.Fatalf("Latency = %s, want the summed invocation latency", report.Latency)
	}

	for _, invocation := range runner.Invocations() {
		if len(invocation.Args) >= 5 && invocation.Args[2] == "shell" && invocation.Args[3] == "getprop" {
			if !isAllowedProperty(invocation.Args[4]) {
				t.Fatalf("health read a property outside the allowlist: %q", invocation.Args[4])
			}
		}
	}
}

func TestHealthSurfacesInfrastructureFailure(t *testing.T) {
	runner := NewFakeRunner().
		Respond(deviceArgs(getStateArgv()...), FakeResponse{Err: errors.New("exec: \"adb\": executable file not found")})
	adapter := newTestAdapter(t, runner)

	report, err := adapter.Health(context.Background(), testSerial)
	if err == nil {
		t.Fatal("Health() = nil, want an infrastructure failure")
	}
	if FailureClassOf(err) != domain.FailureInfrastructure || report.FailureClass != domain.FailureInfrastructure {
		t.Fatalf("report = %+v err = %v, want infrastructure_error", report, err)
	}
}

func TestTransportIdentityIsSeparateFromDeviceIdentity(t *testing.T) {
	runner := NewFakeRunner().RespondStdout(devicesArgv(), devicesFixture)
	adapter := newTestAdapter(t, runner)

	transportID, err := adapter.TransportIdentity(context.Background(), testSerial)
	if err != nil {
		t.Fatalf("TransportIdentity() = %v", err)
	}
	if transportID != "2" {
		t.Fatalf("TransportIdentity() = %q, want 2", transportID)
	}
	if transportID == testSerial {
		t.Fatal("transport identity must not be the device serial")
	}
}

func TestTransportIdentityReportsAMissingIdentifier(t *testing.T) {
	runner := NewFakeRunner().RespondStdout(devicesArgv(),
		"List of devices attached\nR5CT30ABCD             device usb:1-2\n")
	adapter := newTestAdapter(t, runner)

	if _, err := adapter.TransportIdentity(context.Background(), testSerial); FailureClassOf(err) != domain.FailureObservation {
		t.Fatalf("TransportIdentity() failure class = %q, want observation_error", FailureClassOf(err))
	}
}

func pngFixture() []byte {
	return append(append([]byte(nil), pngMagic...), []byte("drift-test-pixels")...)
}

func TestScreenshotHashesBoundedPNGBytes(t *testing.T) {
	payload := pngFixture()
	runner := NewFakeRunner().
		Respond(deviceArgs(screencapArgv()...), FakeResponse{Result: Result{Stdout: payload, Duration: 40 * time.Millisecond}})
	adapter := newTestAdapter(t, runner)

	capture, err := adapter.Screenshot(context.Background(), testSerial)
	if err != nil {
		t.Fatalf("Screenshot() = %v", err)
	}

	sum := sha256.Sum256(payload)
	if capture.Hash != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("Hash = %q, want the sha256 of the payload", capture.Hash)
	}
	if string(capture.PNG) != string(payload) {
		t.Fatal("PNG payload was not returned intact")
	}
	if capture.CorrelationID != "corr-1" || !capture.CapturedAt.Equal(testCapturedAt) {
		t.Fatalf("capture = %+v, want the seeded correlation and clock", capture)
	}
	if capture.Latency != 40*time.Millisecond || capture.FailureClass != "" {
		t.Fatalf("capture = %+v, want a clean capture", capture)
	}
	if strings.Join(runner.Invocations()[0].Args, " ") != "-s "+testSerial+" exec-out screencap -p" {
		t.Fatalf("args = %q", runner.Invocations()[0].Args)
	}
}

func TestScreenshotRejectsOversizedAndNonPNGPayloads(t *testing.T) {
	t.Run("over the bound", func(t *testing.T) {
		payload := append(pngFixture(), make([]byte, 64)...)
		runner := NewFakeRunner().
			Respond(deviceArgs(screencapArgv()...), FakeResponse{Result: Result{Stdout: payload}})
		adapter := newTestAdapter(t, runner, func(a *Adapter) error { return WithMaxScreenshotBytes(16)(a) })

		capture, err := adapter.Screenshot(context.Background(), testSerial)
		if !errors.Is(err, ErrScreenshotInvalid) {
			t.Fatalf("Screenshot() = %v, want ErrScreenshotInvalid", err)
		}
		if capture.PNG != nil || capture.FailureClass != domain.FailureObservation {
			t.Fatalf("capture = %+v, want no bytes and an observation failure", capture)
		}
	})

	t.Run("truncated by the runner", func(t *testing.T) {
		runner := NewFakeRunner().
			Respond(deviceArgs(screencapArgv()...), FakeResponse{Result: Result{Stdout: pngFixture(), StdoutTruncated: true}})
		adapter := newTestAdapter(t, runner)

		if _, err := adapter.Screenshot(context.Background(), testSerial); !errors.Is(err, ErrScreenshotInvalid) {
			t.Fatalf("Screenshot() = %v, want ErrScreenshotInvalid", err)
		}
	})

	t.Run("not a PNG", func(t *testing.T) {
		runner := NewFakeRunner().
			Respond(deviceArgs(screencapArgv()...), FakeResponse{Result: Result{Stdout: []byte("error: closed")}})
		adapter := newTestAdapter(t, runner)

		capture, err := adapter.Screenshot(context.Background(), testSerial)
		if !errors.Is(err, ErrScreenshotInvalid) {
			t.Fatalf("Screenshot() = %v, want ErrScreenshotInvalid", err)
		}
		if capture.FailureClass != domain.FailureObservation {
			t.Fatalf("FailureClass = %q, want observation_error", capture.FailureClass)
		}
	})
}

// TestReattachIsReadOnlyAndUsedAtMostOncePerChange pins the documented rule:
// one reattach per observed transport-identity change, and no mutating adb
// command is ever issued.
func TestReattachIsReadOnlyAndUsedAtMostOncePerChange(t *testing.T) {
	runner := NewFakeRunner().RespondStdout(devicesArgv(), devicesFixture)
	adapter := newTestAdapter(t, runner)
	ctx := context.Background()

	device, err := adapter.ReattachReadOnly(ctx, testSerial, "1")
	if err != nil {
		t.Fatalf("first ReattachReadOnly() = %v", err)
	}
	if device.TransportID != "2" {
		t.Fatalf("TransportID = %q, want 2", device.TransportID)
	}

	if _, err := adapter.ReattachReadOnly(ctx, testSerial, "1"); !errors.Is(err, ErrReattachExhausted) {
		t.Fatalf("second ReattachReadOnly() = %v, want ErrReattachExhausted", err)
	}
	if _, err := adapter.ReattachReadOnly(ctx, testSerial, "2"); !errors.Is(err, ErrNoTransportChange) {
		t.Fatalf("ReattachReadOnly(current id) = %v, want ErrNoTransportChange", err)
	}

	// A genuinely new change permits exactly one more reattach.
	runner.RespondStdout(devicesArgv(),
		"List of devices attached\nR5CT30ABCD             device usb:1-4 transport_id:9\n")
	if _, err := adapter.ReattachReadOnly(ctx, testSerial, "2"); err != nil {
		t.Fatalf("ReattachReadOnly(new change) = %v", err)
	}
	if _, err := adapter.ReattachReadOnly(ctx, testSerial, "2"); !errors.Is(err, ErrReattachExhausted) {
		t.Fatalf("second ReattachReadOnly(new change) = %v, want ErrReattachExhausted", err)
	}

	for _, invocation := range runner.Invocations() {
		joined := strings.Join(invocation.Args, " ")
		if joined != "devices -l" && !strings.HasSuffix(joined, " shell settings get global device_name") {
			t.Fatalf("reattach issued a non read-only command: %q", joined)
		}
	}
}

func TestReattachRequiresAPreviousTransportIdentity(t *testing.T) {
	runner := NewFakeRunner().RespondStdout(devicesArgv(), devicesFixture)
	adapter := newTestAdapter(t, runner)

	if _, err := adapter.ReattachReadOnly(context.Background(), testSerial, "  "); err == nil {
		t.Fatal("ReattachReadOnly(blank previous id) = nil, want an error")
	}
}

func TestRunAllowlistedRefusesUnapprovedArgv(t *testing.T) {
	runner := NewFakeRunner().RespondDefault(FakeResponse{})
	adapter := newTestAdapter(t, runner)

	for _, args := range [][]string{
		{"shell", "input", "tap", "1"},
		{"shell", "input", "tap", "1", "2", "3"},
		{"shell", "input", "tap", "-1", "2"},
		{"shell", "getprop", "ro.serialno"},
		{"exec-out", "cat", "/data/misc/adb/adb_keys"},
		{"devices", "-l"},
		{"reconnect"},
	} {
		if _, err := adapter.RunAllowlisted(context.Background(), testSerial, args); !errors.Is(err, ErrArgvNotAllowlisted) {
			t.Fatalf("RunAllowlisted(%q) = %v, want ErrArgvNotAllowlisted", args, err)
		}
	}
	if count := len(runner.Invocations()); count != 0 {
		t.Fatalf("runner was invoked %d times for rejected argv, want 0", count)
	}
}

func TestRunAllowlistedPlacesTheSerialInItsOwnToken(t *testing.T) {
	runner := NewFakeRunner().Respond(deviceArgs(getStateArgv()...), FakeResponse{Result: Result{Stdout: []byte("device\n")}})
	adapter := newTestAdapter(t, runner)

	if _, err := adapter.RunAllowlisted(context.Background(), testSerial, getStateArgv()); err != nil {
		t.Fatalf("RunAllowlisted() = %v", err)
	}
	args := runner.Invocations()[0].Args
	if len(args) < 3 || args[0] != "-s" || args[1] != testSerial {
		t.Fatalf("args = %q, want the serial as a discrete token after -s", args)
	}
}

func TestOperationErrorsCarryRedactedStderr(t *testing.T) {
	stderr := "adb: error: failed to authenticate with /Users/operator/.android/adbkey (api_key=TEST_ONLY_sk_live_1234abcd)"
	runner := NewFakeRunner().
		Respond(deviceArgs(getStateArgv()...), FakeResponse{Result: Result{ExitCode: 1, Stderr: []byte(stderr)}})
	adapter := newTestAdapter(t, runner)

	_, err := adapter.Health(context.Background(), testSerial)
	if err != nil {
		t.Fatalf("Health() = %v, want a report rather than a fatal error", err)
	}

	_, runErr := adapter.RunAllowlisted(context.Background(), testSerial, getStateArgv())
	var operationErr *OperationError
	if !errors.As(runErr, &operationErr) {
		t.Fatalf("RunAllowlisted() = %v, want an *OperationError", runErr)
	}
	for _, leaked := range []string{"adbkey", "TEST_ONLY_sk_live_1234abcd"} {
		if strings.Contains(operationErr.Detail, leaked) || strings.Contains(operationErr.Error(), leaked) {
			t.Fatalf("OperationError leaked %q: %s", leaked, operationErr.Error())
		}
	}
	if !strings.Contains(operationErr.Detail, "failed to authenticate") {
		t.Fatalf("Detail dropped the diagnostic: %q", operationErr.Detail)
	}
}

func TestExitStatusClassification(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   domain.FailureClass
	}{
		{name: "device not found", stderr: "adb: error: device 'R5CT30ABCD' not found", want: domain.FailureDeviceOffline},
		{name: "no devices", stderr: "error: no devices/emulators found", want: domain.FailureDeviceOffline},
		{name: "offline", stderr: "error: device offline", want: domain.FailureDeviceOffline},
		{name: "unauthorized", stderr: "error: device unauthorized.", want: domain.FailureTransport},
		{name: "protocol fault", stderr: "error: protocol fault (couldn't read status)", want: domain.FailureTransport},
		{name: "daemon", stderr: "adb: cannot connect to daemon at tcp:5037", want: domain.FailureInfrastructure},
		{name: "unknown", stderr: "error: something else entirely", want: domain.FailureTransport},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := NewFakeRunner().
				Respond(deviceArgs(getStateArgv()...), FakeResponse{Result: Result{ExitCode: 1, Stderr: []byte(test.stderr)}})
			adapter := newTestAdapter(t, runner)

			_, err := adapter.RunAllowlisted(context.Background(), testSerial, getStateArgv())
			if FailureClassOf(err) != test.want {
				t.Fatalf("failure class = %q, want %q", FailureClassOf(err), test.want)
			}
		})
	}
}

func TestOperationTimeoutKillsTheInvocation(t *testing.T) {
	runner := NewFakeRunner().
		Respond(deviceArgs(getStateArgv()...), FakeResponse{Delay: 5 * time.Second})
	adapter := newTestAdapter(t, runner, WithOperationTimeout(50*time.Millisecond))

	_, err := adapter.RunAllowlisted(context.Background(), testSerial, getStateArgv())
	if FailureClassOf(err) != domain.FailureTimeout {
		t.Fatalf("failure class = %q, want timeout (err=%v)", FailureClassOf(err), err)
	}
	if runner.Kills() != 1 {
		t.Fatalf("Kills() = %d, want 1", runner.Kills())
	}
	if invocations := runner.Invocations(); len(invocations) != 1 || !invocations[0].Killed {
		t.Fatalf("invocations = %+v, want one killed invocation", invocations)
	}
}

func TestOperatorCancellationIsDistinctFromTimeout(t *testing.T) {
	runner := NewFakeRunner().
		Respond(deviceArgs(screencapArgv()...), FakeResponse{Delay: 5 * time.Second})
	adapter := newTestAdapter(t, runner)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	defer cancel()

	capture, err := adapter.Screenshot(ctx, testSerial)
	if FailureClassOf(err) != domain.FailureOperatorCancelled {
		t.Fatalf("failure class = %q, want operator_cancelled (err=%v)", FailureClassOf(err), err)
	}
	if capture.FailureClass != domain.FailureOperatorCancelled || capture.PNG != nil {
		t.Fatalf("capture = %+v, want a cancelled capture with no bytes", capture)
	}
	if runner.Kills() != 1 {
		t.Fatalf("Kills() = %d, want 1", runner.Kills())
	}
}

func TestFakeRunnerFailsClosedOnUnscriptedInvocations(t *testing.T) {
	runner := NewFakeRunner()
	adapter := newTestAdapter(t, runner)

	_, err := adapter.Enumerate(context.Background())
	if !errors.Is(err, ErrFakeUnconfigured) {
		t.Fatalf("Enumerate() = %v, want ErrFakeUnconfigured", err)
	}
	if FailureClassOf(err) != domain.FailureInfrastructure {
		t.Fatalf("failure class = %q, want infrastructure_error", FailureClassOf(err))
	}
}

func TestParseVersionFallsBackToTheLegacyLine(t *testing.T) {
	if got := parseVersion([]byte("Android Debug Bridge version 1.0.41\n")); got != "1.0.41" {
		t.Fatalf("parseVersion() = %q, want 1.0.41", got)
	}
	if got := parseVersion([]byte("unexpected output")); got != "" {
		t.Fatalf("parseVersion() = %q, want an empty result", got)
	}
}
