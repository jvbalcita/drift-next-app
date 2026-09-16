package transportconnect_test

import (
	"context"
	"strings"
	"testing"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/edge/lab"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	transportconnect "drift.local/drift-next/internal/transport/connect"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	labWorkspace = "workspace-1"
	labOperator  = "operator-1"
	labSerial    = "mock-device-alpha"
)

// stubLabAdapter records whether the handler reached the application service and
// returns scripted classified failures.
type stubLabAdapter struct {
	calls int
	err   error
}

func (s *stubLabAdapter) Status(context.Context) lab.Status {
	s.calls++
	return lab.Status{Mode: lab.ModeMock, Readiness: lab.ReadinessUnavailable}
}

func (s *stubLabAdapter) CaptureObservation(context.Context, lab.CaptureRequest) (lab.ObservationBundle, error) {
	s.calls++
	return lab.ObservationBundle{}, s.err
}

func (s *stubLabAdapter) Events() []lab.Event {
	s.calls++
	return nil
}

func newMockLabService(t *testing.T) *lab.Service {
	t.Helper()
	service, err := lab.NewService()
	if err != nil {
		t.Fatalf("lab.NewService() error = %v", err)
	}
	return service
}

func labRequestContext(idempotencyKey string) *driftv1.RequestContext {
	return &driftv1.RequestContext{RequestId: "request-1", IdempotencyKey: idempotencyKey}
}

func TestLabHandlerRejectsMissingWorkspaceBeforeReachingTheService(t *testing.T) {
	stub := &stubLabAdapter{}
	handler := transportconnect.NewLabAdapterHandler(stub)

	_, err := handler.GetLabStatus(context.Background(), connectrpc.NewRequest(&driftv1.GetLabStatusRequest{}))
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("GetLabStatus() code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument)
	}
	if stub.calls != 0 {
		t.Fatal("GetLabStatus() reached the lab service for invalid input")
	}
}

func TestLabHandlerRequiresAnOperatorRequestIdentityAndTarget(t *testing.T) {
	stub := &stubLabAdapter{}
	handler := transportconnect.NewLabAdapterHandler(stub)
	workspace := &driftv1.WorkspaceRef{WorkspaceId: labWorkspace}

	if _, err := handler.CaptureLabObservation(context.Background(), connectrpc.NewRequest(&driftv1.CaptureLabObservationRequest{
		Workspace:  workspace,
		Context:    labRequestContext("key-1"),
		OperatorId: "",
		Serial:     labSerial,
	})); connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("CaptureLabObservation() without an operator code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument)
	}
	if _, err := handler.CaptureLabObservation(context.Background(), connectrpc.NewRequest(&driftv1.CaptureLabObservationRequest{
		Workspace:  workspace,
		OperatorId: labOperator,
		Serial:     labSerial,
		Context:    &driftv1.RequestContext{RequestId: "request-1"},
	})); connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("CaptureLabObservation() without an idempotency key code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument)
	}
	// The capture names its own target: an unnamed one never reaches the service.
	if _, err := handler.CaptureLabObservation(context.Background(), connectrpc.NewRequest(&driftv1.CaptureLabObservationRequest{
		Workspace:  workspace,
		Context:    labRequestContext("key-1"),
		OperatorId: labOperator,
	})); connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("CaptureLabObservation() without a serial code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument)
	}
	if stub.calls != 0 {
		t.Fatal("lab handler reached the service for invalid input")
	}
}

func TestLabHandlerRejectsAnUnsafeCaptureTimeoutAndEventLimit(t *testing.T) {
	stub := &stubLabAdapter{}
	handler := transportconnect.NewLabAdapterHandler(stub)
	workspace := &driftv1.WorkspaceRef{WorkspaceId: labWorkspace}

	if _, err := handler.CaptureLabObservation(context.Background(), connectrpc.NewRequest(&driftv1.CaptureLabObservationRequest{
		Workspace:  workspace,
		OperatorId: labOperator,
		Serial:     labSerial,
		Context:    labRequestContext("key-1"),
		TimeoutMs:  uint32(lab.MaxCaptureTimeout.Milliseconds()) + 1,
	})); connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("CaptureLabObservation() code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument)
	}
	if _, err := handler.ListLabEvents(context.Background(), connectrpc.NewRequest(&driftv1.ListLabEventsRequest{
		Workspace: workspace,
		Limit:     1000,
	})); connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("ListLabEvents() code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument)
	}
	if stub.calls != 0 {
		t.Fatal("lab handler reached the service for invalid input")
	}
}

func TestLabHandlerReportsAnUnconfiguredService(t *testing.T) {
	handler := transportconnect.NewLabAdapterHandler(nil)

	_, err := handler.GetLabStatus(context.Background(), connectrpc.NewRequest(&driftv1.GetLabStatusRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: labWorkspace},
	}))
	if connectrpc.CodeOf(err) != connectrpc.CodeUnavailable {
		t.Fatalf("GetLabStatus() code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeUnavailable)
	}
}

func TestLabHandlerMapsClassifiedServiceFailuresToStableCodes(t *testing.T) {
	for name, testCase := range map[string]struct {
		err  error
		want connectrpc.Code
	}{
		"precondition": {
			err:  platformerrors.New(platformerrors.CodePreconditionFailed, "the named target is not attached"),
			want: connectrpc.CodeFailedPrecondition,
		},
		"postcondition": {
			err:  platformerrors.New(platformerrors.CodePostconditionFailed, "lab observation did not satisfy its postcondition"),
			want: connectrpc.CodeFailedPrecondition,
		},
		"not found": {
			err:  platformerrors.New(platformerrors.CodeNotFound, "lab target was not observed"),
			want: connectrpc.CodeNotFound,
		},
		"indeterminate": {
			err:  platformerrors.New(platformerrors.CodeIndeterminateCompletion, "outcome is unknown"),
			want: connectrpc.CodeUnknown,
		},
		"denied": {
			err:  platformerrors.New(platformerrors.CodePolicyDenied, "operator is not permitted to perform this lab action"),
			want: connectrpc.CodePermissionDenied,
		},
		"ambiguous": {
			err:  platformerrors.New(platformerrors.CodeAmbiguousTarget, "more than one device is attached"),
			want: connectrpc.CodeFailedPrecondition,
		},
	} {
		t.Run(name, func(t *testing.T) {
			handler := transportconnect.NewLabAdapterHandler(&stubLabAdapter{err: testCase.err})

			_, err := handler.CaptureLabObservation(context.Background(), connectrpc.NewRequest(&driftv1.CaptureLabObservationRequest{
				Workspace:  &driftv1.WorkspaceRef{WorkspaceId: labWorkspace},
				OperatorId: labOperator,
				Serial:     labSerial,
				Context:    labRequestContext("key-1"),
			}))
			if connectrpc.CodeOf(err) != testCase.want {
				t.Fatalf("CaptureLabObservation() code = %v, want %v", connectrpc.CodeOf(err), testCase.want)
			}
		})
	}
}

func TestLabHandlerProjectsAnObservationWithoutRawEvidence(t *testing.T) {
	handler := transportconnect.NewLabAdapterHandler(newMockLabService(t))
	ctx := context.Background()
	workspace := &driftv1.WorkspaceRef{WorkspaceId: labWorkspace}

	captured, err := handler.CaptureLabObservation(ctx, connectrpc.NewRequest(&driftv1.CaptureLabObservationRequest{
		Workspace:  workspace,
		Context:    labRequestContext("key-1"),
		OperatorId: labOperator,
		Serial:     labSerial,
	}))
	if err != nil {
		t.Fatalf("CaptureLabObservation() error = %v", err)
	}
	observation := captured.Msg.GetObservation()
	if observation.GetSerial() != labSerial {
		t.Fatalf("observation serial = %q, want the explicitly named target", observation.GetSerial())
	}
	if !observation.GetPostconditionVerified() || observation.GetIndeterminate() {
		t.Fatalf("observation = %+v, want a verified determinate capture", observation)
	}
	if !strings.HasPrefix(observation.GetScreenshot().GetContentHash(), "sha256:") {
		t.Fatalf("observation screenshot = %+v, want a content hash reference", observation.GetScreenshot())
	}
	if observation.GetPreviewBase64() != "" {
		t.Fatal("observation exposed an inline preview even though previews are disabled by default")
	}
	if observation.GetHierarchySummary() == "" || len(observation.GetEvents()) == 0 {
		t.Fatalf("observation = %+v, want a bounded hierarchy summary and audit events", observation)
	}
	if !strings.Contains(observation.GetCapturedAt(), "T") {
		t.Fatalf("observation.CapturedAt = %q, want RFC3339 UTC text", observation.GetCapturedAt())
	}
	if status := captured.Msg.GetStatus(); status.GetReadiness() != driftv1.LabReadiness_LAB_READINESS_READY {
		t.Fatalf("CaptureLabObservation() readiness = %v, want ready", status.GetReadiness())
	}

	events, err := handler.ListLabEvents(ctx, connectrpc.NewRequest(&driftv1.ListLabEventsRequest{Workspace: workspace}))
	if err != nil {
		t.Fatalf("ListLabEvents() error = %v", err)
	}
	if len(events.Msg.GetEvents()) == 0 {
		t.Fatal("ListLabEvents() returned no audit records")
	}
	for _, event := range events.Msg.GetEvents() {
		if event.GetName() == driftv1.LabEventName_LAB_EVENT_NAME_UNSPECIFIED {
			t.Fatalf("event %+v has no typed name", event)
		}
	}
}

func TestLabHandlerRefusesACaptureForASerialThatIsNotAttached(t *testing.T) {
	handler := transportconnect.NewLabAdapterHandler(newMockLabService(t))
	ctx := context.Background()
	workspace := &driftv1.WorkspaceRef{WorkspaceId: labWorkspace}

	if _, err := handler.CaptureLabObservation(ctx, connectrpc.NewRequest(&driftv1.CaptureLabObservationRequest{
		Workspace:  workspace,
		Context:    labRequestContext("key-1"),
		OperatorId: labOperator,
		Serial:     "mock-device-gamma",
	})); connectrpc.CodeOf(err) != connectrpc.CodeFailedPrecondition {
		t.Fatalf("CaptureLabObservation() for an unattached serial code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition)
	}
}

// The duplicate discovery/confirmation lifecycle is gone from the contract, and
// the retired field numbers and names stay reserved so no future change can
// reuse them.
func TestLabAdapterContractNoLongerExposesTheConfirmLifecycle(t *testing.T) {
	file := driftv1.File_drift_v1_lab_adapter_proto
	service := file.Services().ByName("LabAdapterService")
	if service == nil {
		t.Fatal("LabAdapterService is missing from the contract")
	}
	for _, removed := range []string{"DiscoverLabDevices", "ConfirmLabTarget", "ClearLabTarget"} {
		if method := service.Methods().ByName(protoreflect.Name(removed)); method != nil {
			t.Fatalf("LabAdapterService still exposes the retired RPC %q", removed)
		}
	}
	for _, removed := range []string{"DiscoverLabDevicesRequest", "ConfirmLabTargetRequest", "ClearLabTargetRequest"} {
		if message := file.Messages().ByName(protoreflect.Name(removed)); message != nil {
			t.Fatalf("the contract still declares the retired message %q", removed)
		}
	}

	status := file.Messages().ByName("LabStatus")
	for name, number := range map[protoreflect.Name]protoreflect.FieldNumber{
		"confirmed_serial":       5,
		"confirmed_display_name": 6,
		"stable_identity":        7,
		"transport_id":           8,
	} {
		if field := status.Fields().ByName(name); field != nil {
			t.Fatalf("LabStatus still declares the retired confirm field %q", name)
		}
		if !status.ReservedNames().Has(name) {
			t.Fatalf("LabStatus does not reserve the retired field name %q", name)
		}
		if !status.ReservedRanges().Has(number) {
			t.Fatalf("LabStatus does not reserve the retired field number %d", number)
		}
	}

	eventNames := file.Enums().ByName("LabEventName")
	for name, number := range map[protoreflect.Name]protoreflect.EnumNumber{
		"LAB_EVENT_NAME_TARGET_CONFIRMATION":   1,
		"LAB_EVENT_NAME_OPERATOR_CONFIRMATION": 11,
	} {
		if value := eventNames.Values().ByName(name); value != nil {
			t.Fatalf("LabEventName still declares the retired confirm event %q", name)
		}
		if !eventNames.ReservedNames().Has(name) {
			t.Fatalf("LabEventName does not reserve the retired event name %q", name)
		}
		if !eventNames.ReservedRanges().Has(number) {
			t.Fatalf("LabEventName does not reserve the retired event number %d", number)
		}
	}
}

func TestLabAdapterContractExposesNoRawEvidenceOrExecutionAuthority(t *testing.T) {
	bundle := driftv1.File_drift_v1_lab_adapter_proto.Messages().ByName("LabObservationBundle").Fields()
	for _, forbidden := range []string{"hierarchy_xml", "screenshot_bytes_payload", "png", "raw_output", "device_path", "command", "action", "credential", "token"} {
		if field := bundle.ByName(protoreflect.Name(forbidden)); field != nil {
			t.Fatalf("LabObservationBundle must not expose %q", forbidden)
		}
	}
	request := driftv1.File_drift_v1_lab_adapter_proto.Messages().ByName("CaptureLabObservationRequest").Fields()
	for _, forbidden := range []string{"command", "argv", "shell", "lease", "fencing_token", "adb_path"} {
		if field := request.ByName(protoreflect.Name(forbidden)); field != nil {
			t.Fatalf("CaptureLabObservationRequest must not expose %q", forbidden)
		}
	}
}
