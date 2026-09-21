package lab

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"sync"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adapter"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/uiautomator"
)

const (
	// MockAdapterVersion identifies the deterministic fixture adapter so an
	// operator can never mistake mock evidence for lab evidence.
	MockAdapterVersion = "p13-mock.1.0.0"

	// MockPlatformToolsVersion is the fixture platform-tools version.
	MockPlatformToolsVersion = "0.0.0-mock"
)

// DefaultMockCandidates returns the deterministic discovery fixtures. Two
// candidates are attached on purpose: the default mock session therefore
// exercises the rule that a target is never inferred when more than one device
// is present.
func DefaultMockCandidates() []adb.DiscoveredDevice {
	return []adb.DiscoveredDevice{
		{
			Serial:         "mock-device-alpha",
			State:          adb.StateDevice,
			Product:        "mock_alpha",
			Model:          "Mock Alpha",
			Device:         "alpha",
			TransportID:    "1",
			ConnectionType: adb.ConnectionUSB,
		},
		{
			Serial:         "mock-device-beta",
			State:          adb.StateDevice,
			Product:        "mock_beta",
			Model:          "Mock Beta",
			Device:         "beta",
			TransportID:    "2",
			ConnectionType: adb.ConnectionUSB,
		},
	}
}

// mockAdapter answers every lab operation from fixtures. It spawns no process,
// opens no socket, and never reaches adb.
type mockAdapter struct {
	candidates []adb.DiscoveredDevice

	mu         sync.Mutex
	reattached map[string]string
}

func newMockAdapter(candidates []adb.DiscoveredDevice) *mockAdapter {
	if len(candidates) == 0 {
		candidates = DefaultMockCandidates()
	}
	return &mockAdapter{
		candidates: append([]adb.DiscoveredDevice(nil), candidates...),
		reattached: make(map[string]string),
	}
}

func (m *mockAdapter) Version() string { return MockAdapterVersion }

func (m *mockAdapter) ValidateSerial(serial string) error { return adb.ValidateSerial(serial) }

func (m *mockAdapter) PlatformToolsVersion(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", mockError("version", "", domain.FailureOperatorCancelled, err)
	}
	return MockPlatformToolsVersion, nil
}

func (m *mockAdapter) Enumerate(ctx context.Context) ([]adb.DiscoveredDevice, error) {
	if err := ctx.Err(); err != nil {
		return nil, mockError("enumerate", "", domain.FailureOperatorCancelled, err)
	}
	return append([]adb.DiscoveredDevice(nil), m.candidates...), nil
}

func (m *mockAdapter) Health(ctx context.Context, serial string) (adb.HealthReport, error) {
	if err := ctx.Err(); err != nil {
		return adb.HealthReport{}, mockError("health", serial, domain.FailureOperatorCancelled, err)
	}
	candidate, found := m.find(serial)
	if !found {
		return adb.HealthReport{Serial: serial}, mockError("health", serial, domain.FailureDeviceOffline, adb.ErrDeviceNotFound)
	}
	report := adb.HealthReport{
		Serial:               serial,
		TransportID:          candidate.TransportID,
		State:                string(candidate.State),
		AndroidVersion:       "14",
		APILevel:             "34",
		Model:                candidate.Model,
		PlatformToolsVersion: MockPlatformToolsVersion,
		Latency:              0,
	}
	if !candidate.State.Usable() {
		report.FailureClass = domain.FailureTransport
	}
	return report, nil
}

func (m *mockAdapter) Screenshot(ctx context.Context, serial string) (adb.ScreenshotResult, error) {
	if err := ctx.Err(); err != nil {
		return adb.ScreenshotResult{}, mockError("screenshot", serial, domain.FailureOperatorCancelled, err)
	}
	if _, found := m.find(serial); !found {
		return adb.ScreenshotResult{}, mockError("screenshot", serial, domain.FailureDeviceOffline, adb.ErrDeviceNotFound)
	}
	payload := mockScreenshotPayload(serial)
	return adb.ScreenshotResult{
		Serial:  serial,
		PNG:     payload,
		Hash:    adb.HashBytes(payload),
		Latency: 0,
	}, nil
}

// ReattachReadOnly re-reads the fixture transport. Like the real adapter it
// permits at most one reattach per observed change and issues no transport
// command of any kind.
func (m *mockAdapter) ReattachReadOnly(ctx context.Context, serial string, previousTransportID string) (adb.DiscoveredDevice, error) {
	if err := ctx.Err(); err != nil {
		return adb.DiscoveredDevice{}, mockError("reattach", serial, domain.FailureOperatorCancelled, err)
	}
	candidate, found := m.find(serial)
	if !found {
		return adb.DiscoveredDevice{}, mockError("reattach", serial, domain.FailureDeviceOffline, adb.ErrDeviceNotFound)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reattached[serial] == previousTransportID {
		return candidate, mockError("reattach", serial, domain.FailureTransport, adb.ErrReattachExhausted)
	}
	m.reattached[serial] = previousTransportID
	return candidate, nil
}

func (m *mockAdapter) Capture(ctx context.Context, serial string) (uiautomator.HierarchyCapture, error) {
	if err := ctx.Err(); err != nil {
		return uiautomator.HierarchyCapture{}, mockError("uiautomator-capture", serial, domain.FailureOperatorCancelled, err)
	}
	if _, found := m.find(serial); !found {
		return uiautomator.HierarchyCapture{}, mockError("uiautomator-capture", serial, domain.FailureDeviceOffline, adb.ErrDeviceNotFound)
	}
	nodes := mockNodes()
	return uiautomator.HierarchyCapture{
		Serial:          serial,
		Source:          uiautomator.Source,
		ProtocolVersion: uiautomator.ProtocolVersion,
		AdapterVersion:  MockAdapterVersion,
		NodeCount:       len(nodes),
		MaxDepth:        2,
		Sanitized:       true,
		FreshnessToken:  "sha256:" + mockHashHex("hierarchy:"+serial),
		Nodes:           nodes,
		RawSize:         512,
		Latency:         0,
	}, nil
}

func (m *mockAdapter) find(serial string) (adb.DiscoveredDevice, bool) {
	for _, candidate := range m.candidates {
		if candidate.Serial == serial {
			return candidate, true
		}
	}
	return adb.DiscoveredDevice{}, false
}

func mockError(op, serial string, class domain.FailureClass, cause error) error {
	return &adb.OperationError{Op: "mock-" + op, Serial: serial, FailureClass: class, Cause: cause}
}

// mockScreenshotPayload returns a deterministic synthetic screen as a real PNG.
// The payload is fixture material: it carries no screen content and no device
// data, and it is generated from the device's own name so two mock devices are
// two different pictures.
//
// It is a decodable image rather than "PNG-shaped bytes" because the product now
// PARSES a capture: the fleet grid's stills are produced by decoding the capture
// and re-encoding it at a level, so a fixture that only carried the PNG signature
// would make every mock device's tile a failed capture - a fixture that cannot
// exercise the path it exists to stand in for.
func mockScreenshotPayload(serial string) []byte {
	const (
		width  = 90
		height = 200
	)
	seed := mockHashHex("screenshot:" + serial)
	screen := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// A coarse two-dimensional pattern derived from the device's own name:
			// flat enough to compress, varied enough that a level's downscale is
			// actually exercised rather than averaged into one colour.
			screen.SetRGBA(x, y, color.RGBA{
				R: uint8(x*2) ^ uint8(seed[0]),
				G: uint8(y) ^ uint8(seed[1]),
				B: uint8(x^y) ^ uint8(seed[2]),
				A: 0xff,
			})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, screen); err != nil {
		// A fixture PNG that cannot be encoded is a fixture defect rather than a
		// device condition: this path is deterministic and reaches no device.
		panic("mock screenshot fixture could not be encoded: " + err.Error())
	}
	return buffer.Bytes()
}

func mockNodes() []adapter.TargetNode {
	return []adapter.TargetNode{
		{
			ResourceID:         "com.example.mock:id/root",
			ClassName:          "android.widget.FrameLayout",
			ContextFingerprint: "sha256:" + mockHashHex("root")[:32],
			Bounds:             [4]int{0, 0, 1080, 2400},
			Enabled:            true,
		},
		{
			ResourceID:         "com.example.mock:id/title",
			StableText:         "Mock Screen",
			ClassName:          "android.widget.TextView",
			ContextFingerprint: "sha256:" + mockHashHex("title")[:32],
			Bounds:             [4]int{40, 120, 1040, 220},
			Enabled:            true,
		},
		{
			ResourceID:         "com.example.mock:id/action",
			AccessibilityLabel: "Continue",
			ClassName:          "android.widget.Button",
			ContextFingerprint: "sha256:" + mockHashHex("action")[:32],
			Bounds:             [4]int{40, 2200, 1040, 2340},
			Actionable:         true,
			Enabled:            true,
		},
	}
}

func mockHashHex(material string) string {
	sum := sha256.Sum256([]byte(material))
	return hex.EncodeToString(sum[:])
}
