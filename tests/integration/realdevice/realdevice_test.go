//go:build realdevice

package realdevice_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/gen/go/drift/v1/driftv1connect"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/edge/registration"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/service"
	store "drift.local/drift-next/internal/store/sqlite"
	realdevice "drift.local/drift-next/tests/integration/realdevice"
)

const attendedOperator = "op-1"
const attendedToken = "realdevice-attended-token"

func TestAttendedRealDeviceRegistrationAndObservation(t *testing.T) {
	serials := attachedDeviceSerials(t)
	selection, err := realdevice.SelectSerial(serials, os.Getenv(realdevice.EnvSerial))
	if err != nil {
		t.Fatal(err)
	}
	if selection.Skip != "" {
		t.Skipf("skipping real-device test: %s", selection.Skip)
	}
	serial := selection.Serial
	if err := adb.ValidateSerial(serial); err != nil {
		t.Fatalf("%s is not a valid device serial: %v", realdevice.EnvSerial, err)
	}

	adbPath := requireAdbExecutable(t)
	t.Setenv(lab.EnvLabMode, "1")
	t.Setenv(lab.EnvADBPath, adbPath)
	t.Setenv(lab.EnvLabToken, attendedToken)

	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "drift.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	workspace := organizations.Workspace{ID: "workspace-lab-local", Name: "Local Workspace", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", attendedOperator); err != nil {
		t.Fatal(err)
	}

	labService, err := lab.NewServiceFromEnv(os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	if mode := labService.Status(context.Background()).Mode; mode != lab.ModeLab {
		t.Fatalf("service mode = %q, want %q", mode, lab.ModeLab)
	}
	registrationService := registration.NewService(registration.Config{
		MaxRegisteredDevices: 0,
		Probe: registration.LabStatusProbe{
			Source:       labService,
			AllowedPorts: []uint16{5555},
		},
	})
	server := httptest.NewServer(service.NewHTTPServer("realdevice", "127.0.0.1:0",
		service.LabAdapterRoute(labService, attendedToken),
		service.LabRegistrationRouteWithStore(registrationService, db, attendedToken, []uint16{5555}),
	).Handler)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	workspaceRef := &driftv1.WorkspaceRef{WorkspaceId: string(workspace.ID)}
	adapter := driftv1connect.NewLabAdapterServiceClient(http.DefaultClient, server.URL, connectrpc.WithInterceptors(labTokenInterceptor(attendedToken)))
	registrar := driftv1connect.NewLabRegistrationServiceClient(http.DefaultClient, server.URL, connectrpc.WithInterceptors(labTokenInterceptor(attendedToken)))

	discovered, err := adapter.DiscoverLabDevices(ctx, connectrpc.NewRequest(&driftv1.DiscoverLabDevicesRequest{
		Workspace:  workspaceRef,
		Context:    requestContext("realdevice-discover-1", ""),
		OperatorId: attendedOperator,
	}))
	if err != nil {
		t.Fatalf("DiscoverLabDevices: %v", err)
	}
	status := discovered.Msg.GetStatus()
	if status.GetMode() != driftv1.LabMode_LAB_MODE_LAB {
		t.Fatalf("DiscoverLabDevices mode = %v, want lab", status.GetMode())
	}
	if status.GetConfirmedSerial() != "" {
		t.Fatal("DiscoverLabDevices confirmed a target; discovery must never confirm")
	}
	candidate := requireDiscoveredTarget(t, status.GetDiscovered(), serial)
	t.Logf("target %s is enumerated: state=%s connection=%s", maskSerial(candidate.GetSerial()), candidate.GetConnectionState(), candidate.GetConnectionType())

	confirmed, err := adapter.ConfirmLabTarget(ctx, connectrpc.NewRequest(&driftv1.ConfirmLabTargetRequest{
		Workspace:        workspaceRef,
		Context:          requestContext("realdevice-confirm-1", ""),
		Serial:           serial,
		DisplayName:      "Attended Device",
		ConfirmationText: serial,
		OperatorId:       attendedOperator,
		Reason:           "operator-approved attended registration",
	}))
	if err != nil {
		t.Fatalf("ConfirmLabTarget: %v", err)
	}
	t.Cleanup(func() {
		clearCtx, clearCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer clearCancel()
		if _, clearErr := adapter.ClearLabTarget(clearCtx, connectrpc.NewRequest(&driftv1.ClearLabTargetRequest{
			Workspace:  workspaceRef,
			Context:    requestContext("realdevice-clear-cleanup", ""),
			OperatorId: attendedOperator,
			Reason:     "attended-test cleanup",
		})); clearErr != nil {
			t.Logf("ClearLabTarget cleanup: %v", clearErr)
		}
	})
	confirmedStatus := confirmed.Msg.GetStatus()
	if confirmedStatus.GetReadiness() != driftv1.LabReadiness_LAB_READINESS_READY {
		t.Fatalf("ConfirmLabTarget readiness = %v, want ready", confirmedStatus.GetReadiness())
	}
	if confirmedStatus.GetConfirmedSerial() != serial {
		t.Fatal("confirmed serial does not match the operator-supplied target")
	}
	if confirmedStatus.GetStableIdentity() == "" || confirmedStatus.GetStableIdentity() == confirmedStatus.GetTransportId() {
		t.Fatal("stable identity must never equal the mutable transport identity")
	}

	captured, err := adapter.CaptureLabObservation(ctx, connectrpc.NewRequest(&driftv1.CaptureLabObservationRequest{
		Workspace:  workspaceRef,
		Context:    requestContext("realdevice-capture-1", "realdevice-capture-1"),
		Serial:     serial,
		OperatorId: attendedOperator,
		TimeoutMs:  60000,
	}))
	if err != nil {
		t.Fatalf("CaptureLabObservation: %v", err)
	}
	observation := captured.Msg.GetObservation()
	assertObservation(t, observation, serial, confirmedStatus.GetStableIdentity())
	t.Logf("observation identity=%s health=%s screenshot=%s nodes=%d complete=%t",
		maskSerial(observation.GetStableIdentity()),
		observation.GetHealthState(),
		observation.GetScreenshot().GetContentHash(),
		observation.GetNodeCount(),
		observation.GetHierarchyComplete(),
	)

	for _, extra := range serials {
		if extra == serial {
			continue
		}
		if _, extraErr := adapter.CaptureLabObservation(ctx, connectrpc.NewRequest(&driftv1.CaptureLabObservationRequest{
			Workspace:  workspaceRef,
			Context:    requestContext("realdevice-capture-extra", "realdevice-capture-extra"),
			Serial:     extra,
			OperatorId: attendedOperator,
			TimeoutMs:  5000,
		})); extraErr == nil {
			t.Fatal("CaptureLabObservation succeeded for an unselected serial; fan-out is forbidden")
		}
	}

	connectionType := candidate.GetConnectionType()
	if connectionType == "" {
		connectionType = "usb"
	}
	verified, err := registrar.VerifyLabProvisioning(ctx, connectrpc.NewRequest(&driftv1.VerifyLabProvisioningRequest{
		Workspace:      workspaceRef,
		Context:        requestContext("realdevice-verify-1", ""),
		OperatorId:     attendedOperator,
		Serial:         serial,
		TransportId:    confirmedStatus.GetTransportId(),
		ConnectionType: connectionType,
	}))
	if err != nil {
		t.Fatalf("VerifyLabProvisioning: %v", err)
	}
	if !verified.Msg.GetReadiness().GetReady() {
		t.Fatalf("VerifyLabProvisioning ready = false notes=%v", verified.Msg.GetReadiness().GetNotes())
	}

	approved, err := registrar.ApproveLabProvisioning(ctx, connectrpc.NewRequest(&driftv1.ApproveLabProvisioningRequest{
		Workspace:  workspaceRef,
		Context:    requestContext("realdevice-approve-1", ""),
		OperatorId: attendedOperator,
		Serial:     serial,
		Reason:     "operator-approved attended registration",
	}))
	if err != nil {
		t.Fatalf("ApproveLabProvisioning: %v", err)
	}
	if approved.Msg.GetRegistration().GetState() != driftv1.LabRegistrationState_LAB_REGISTRATION_STATE_APPROVED {
		t.Fatalf("approve state = %v", approved.Msg.GetRegistration().GetState())
	}
	if approved.Msg.GetRegistration().GetMockLabeled() {
		t.Fatal("connect path must not mock-label registrations")
	}

	registered, err := registrar.RegisterLabDevice(ctx, connectrpc.NewRequest(&driftv1.RegisterLabDeviceRequest{
		Workspace:   workspaceRef,
		Context:     requestContext("realdevice-register-1", ""),
		OperatorId:  attendedOperator,
		Serial:      serial,
		DisplayName: "Attended Device",
	}))
	if err != nil {
		t.Fatalf("RegisterLabDevice: %v", err)
	}
	record := registered.Msg.GetRegistration()
	if record.GetDeviceId() == "" || record.GetEndpointId() == "" {
		t.Fatalf("RegisterLabDevice missing durable identities: %#v", record)
	}
	if record.GetDeviceId() == record.GetEndpointId() {
		t.Fatal("device identity must stay separate from endpoint identity")
	}
	if record.GetState() != driftv1.LabRegistrationState_LAB_REGISTRATION_STATE_REGISTERED {
		t.Fatalf("register state = %v", record.GetState())
	}

	if _, err := adapter.ClearLabTarget(ctx, connectrpc.NewRequest(&driftv1.ClearLabTargetRequest{
		Workspace:  workspaceRef,
		Context:    requestContext("realdevice-clear-1", ""),
		OperatorId: attendedOperator,
		Reason:     "clear confirmed target before stale capture",
	})); err != nil {
		t.Fatalf("ClearLabTarget: %v", err)
	}
	if _, staleErr := adapter.CaptureLabObservation(ctx, connectrpc.NewRequest(&driftv1.CaptureLabObservationRequest{
		Workspace:  workspaceRef,
		Context:    requestContext("realdevice-capture-stale", "realdevice-capture-stale"),
		Serial:     serial,
		OperatorId: attendedOperator,
		TimeoutMs:  5000,
	})); staleErr == nil {
		t.Fatal("CaptureLabObservation succeeded after ClearLabTarget; stale observation must be refused")
	} else if connectrpc.CodeOf(staleErr) != connectrpc.CodeFailedPrecondition {
		t.Fatalf("stale CaptureLabObservation code = %v, want %v", connectrpc.CodeOf(staleErr), connectrpc.CodeFailedPrecondition)
	}
}

func requireAdbExecutable(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("adb")
	if err != nil {
		t.Skip("skipping real-device test: adb is not on PATH")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Skipf("skipping real-device test: adb path is not absolute: %v", err)
	}
	info, err := os.Stat(absolute)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		t.Skipf("skipping real-device test: %s is not an executable", absolute)
	}
	return absolute
}

func requireDiscoveredTarget(t *testing.T, candidates []*driftv1.LabDiscoveredDevice, serial string) *driftv1.LabDiscoveredDevice {
	t.Helper()
	if len(candidates) == 0 {
		t.Fatal("no candidate transports were enumerated; nothing to confirm")
	}
	var matches []*driftv1.LabDiscoveredDevice
	for _, candidate := range candidates {
		if candidate.GetSerial() == serial {
			matches = append(matches, candidate)
		}
	}
	switch len(matches) {
	case 0:
		t.Fatalf("selected serial is not among the %d enumerated candidates", len(candidates))
	case 1:
	default:
		t.Fatal("selected serial matched multiple enumerated candidates; the target is ambiguous")
	}
	target := matches[0]
	if !target.GetUsable() {
		t.Fatalf("target is attached but not usable: transport state %q", target.GetConnectionState())
	}
	return target
}

func assertObservation(t *testing.T, observation *driftv1.LabObservationBundle, serial, stableIdentity string) {
	t.Helper()
	if observation.GetSerial() != serial {
		t.Fatal("observation bundle does not describe the confirmed target")
	}
	if observation.GetIndeterminate() {
		t.Fatal("observation bundle is indeterminate; it must not be replayed automatically")
	}
	if !observation.GetPostconditionVerified() {
		t.Fatalf("observation postcondition was not verified (failure class %q)", observation.GetFailureClass())
	}
	if observation.GetScreenshot() == nil || observation.GetScreenshot().GetContentHash() == "" || observation.GetScreenshotBytes() == 0 {
		t.Fatal("screenshot evidence is missing its content hash or byte count")
	}
	if observation.GetPreviewBase64() != "" {
		t.Fatal("observation exposed an inline preview even though previews are disabled by default")
	}
	if !observation.GetHierarchyComplete() || observation.GetNodeCount() == 0 || observation.GetHierarchyFreshnessToken() == "" {
		t.Fatalf("hierarchy is incomplete: summary=%q nodes=%d", observation.GetHierarchySummary(), observation.GetNodeCount())
	}
	if observation.GetStableIdentity() != stableIdentity {
		t.Fatal("observation stable identity does not match the confirmed target")
	}
}

func requestContext(requestID, idempotencyKey string) *driftv1.RequestContext {
	return &driftv1.RequestContext{RequestId: requestID, IdempotencyKey: idempotencyKey, ActorId: attendedOperator}
}

func attachedDeviceSerials(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("adb", "devices").CombinedOutput()
	if err != nil {
		t.Skipf("skipping real-device test: adb devices failed: %v", err)
	}
	var serials []string
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "device" {
			serials = append(serials, fields[0])
		}
	}
	return serials
}

func labTokenInterceptor(token string) connectrpc.UnaryInterceptorFunc {
	return func(next connectrpc.UnaryFunc) connectrpc.UnaryFunc {
		return func(ctx context.Context, request connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
			request.Header().Set(service.LabTokenHeader, token)
			return next(ctx, request)
		}
	}
}

func maskSerial(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return "serial-" + hex.EncodeToString(sum[:4])
}
