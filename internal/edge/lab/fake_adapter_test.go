package lab_test

import (
	"context"
	"errors"
	"sync"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adapter"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/uiautomator"
)

// fakeAdapter is a deterministic stand-in for the ADB and UIAutomator adapters.
// It spawns no process and reaches no device, and it honors cancellation and
// deadlines so timeout behavior is testable without wall-clock flakiness.
type fakeAdapter struct {
	mu         sync.Mutex
	candidates []adb.DiscoveredDevice
	calls      []string

	enumerateErr    error
	reattachErr     error
	reattached      map[string]string
	healthErr       error
	healthDelay     time.Duration
	healthTransport string
	screenshotErr   error
	screenshotDelay time.Duration
	hierarchyErr    error
	hierarchyDelay  time.Duration
	hierarchy       uiautomator.HierarchyCapture
}

func newFakeAdapter(candidates ...adb.DiscoveredDevice) *fakeAdapter {
	if len(candidates) == 0 {
		candidates = []adb.DiscoveredDevice{{
			Serial:         "fakeserial01",
			State:          adb.StateDevice,
			Model:          "Fake",
			TransportID:    "7",
			ConnectionType: adb.ConnectionUSB,
		}}
	}
	return &fakeAdapter{
		candidates: candidates,
		reattached: map[string]string{},
		hierarchy: uiautomator.HierarchyCapture{
			Source:          uiautomator.Source,
			ProtocolVersion: uiautomator.ProtocolVersion,
			NodeCount:       4,
			MaxDepth:        3,
			Sanitized:       true,
			FreshnessToken:  "sha256:fake",
			Nodes:           make([]adapter.TargetNode, 4),
			Latency:         time.Millisecond,
		},
	}
}

func (f *fakeAdapter) Version() string { return "fake.p13" }

func (f *fakeAdapter) ValidateSerial(serial string) error { return adb.ValidateSerial(serial) }

func (f *fakeAdapter) PlatformToolsVersion(context.Context) (string, error) {
	f.record("version")
	return "36.0.0-fake", nil
}

func (f *fakeAdapter) Enumerate(ctx context.Context) ([]adb.DiscoveredDevice, error) {
	f.record("enumerate")
	if f.enumerateErr != nil {
		return nil, f.enumerateErr
	}
	if err := ctx.Err(); err != nil {
		return nil, fakeError("enumerate", "", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]adb.DiscoveredDevice(nil), f.candidates...), nil
}

// ReattachReadOnly mirrors the real adapter: one read-only re-read per observed
// change, and no transport command at all.
func (f *fakeAdapter) ReattachReadOnly(ctx context.Context, serial string, previousTransportID string) (adb.DiscoveredDevice, error) {
	f.record("reattach")
	if err := ctx.Err(); err != nil {
		return adb.DiscoveredDevice{}, fakeError("reattach", serial, err)
	}
	if f.reattachErr != nil {
		return adb.DiscoveredDevice{}, f.reattachErr
	}
	candidate, found := f.find(serial)
	if !found {
		return adb.DiscoveredDevice{}, fakeError("reattach", serial, adb.ErrDeviceNotFound)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reattached[serial] == previousTransportID {
		return candidate, &adb.OperationError{Op: "fake-reattach", Serial: serial, FailureClass: domain.FailureTransport, Cause: adb.ErrReattachExhausted}
	}
	f.reattached[serial] = previousTransportID
	return candidate, nil
}

func (f *fakeAdapter) Health(ctx context.Context, serial string) (adb.HealthReport, error) {
	f.record("health")
	if err := wait(ctx, f.healthDelay); err != nil {
		return adb.HealthReport{Serial: serial}, fakeError("health", serial, err)
	}
	if f.healthErr != nil {
		return adb.HealthReport{Serial: serial}, f.healthErr
	}
	candidate, found := f.find(serial)
	if !found {
		return adb.HealthReport{Serial: serial}, fakeError("health", serial, adb.ErrDeviceNotFound)
	}
	transport := candidate.TransportID
	if f.healthTransport != "" {
		transport = f.healthTransport
	}
	report := adb.HealthReport{
		Serial:               serial,
		TransportID:          transport,
		State:                string(candidate.State),
		AndroidVersion:       "14",
		APILevel:             "34",
		Model:                candidate.Model,
		PlatformToolsVersion: "36.0.0-fake",
		Latency:              time.Millisecond,
	}
	if !candidate.State.Usable() {
		report.FailureClass = domain.FailureTransport
	}
	return report, nil
}

func (f *fakeAdapter) Screenshot(ctx context.Context, serial string) (adb.ScreenshotResult, error) {
	f.record("screenshot")
	if err := wait(ctx, f.screenshotDelay); err != nil {
		return adb.ScreenshotResult{Serial: serial}, fakeError("screenshot", serial, err)
	}
	if f.screenshotErr != nil {
		return adb.ScreenshotResult{Serial: serial}, f.screenshotErr
	}
	payload := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte("fake-screen")...)
	return adb.ScreenshotResult{
		Serial:  serial,
		PNG:     payload,
		Hash:    adb.HashBytes(payload),
		Latency: time.Millisecond,
	}, nil
}

func (f *fakeAdapter) Capture(ctx context.Context, serial string) (uiautomator.HierarchyCapture, error) {
	f.record("hierarchy")
	if err := wait(ctx, f.hierarchyDelay); err != nil {
		return uiautomator.HierarchyCapture{Serial: serial}, fakeError("hierarchy", serial, err)
	}
	if f.hierarchyErr != nil {
		return uiautomator.HierarchyCapture{Serial: serial}, f.hierarchyErr
	}
	capture := f.hierarchy
	capture.Serial = serial
	return capture, nil
}

func (f *fakeAdapter) find(serial string) (adb.DiscoveredDevice, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, candidate := range f.candidates {
		if candidate.Serial == serial {
			return candidate, true
		}
	}
	return adb.DiscoveredDevice{}, false
}

func (f *fakeAdapter) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeAdapter) callCount(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, recorded := range f.calls {
		if recorded == call {
			count++
		}
	}
	return count
}

// wait honors both a scripted delay and the caller's context so a deadline can
// expire mid-operation.
func wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// fakeError mirrors how the real adapters classify host and transport failures.
func fakeError(op, serial string, cause error) error {
	class := domain.FailureTransport
	switch {
	case errors.Is(cause, context.DeadlineExceeded):
		class = domain.FailureTimeout
	case errors.Is(cause, context.Canceled):
		class = domain.FailureOperatorCancelled
	case errors.Is(cause, adb.ErrDeviceNotFound):
		class = domain.FailureDeviceOffline
	}
	return &adb.OperationError{Op: "fake-" + op, Serial: serial, FailureClass: class, Cause: cause}
}
