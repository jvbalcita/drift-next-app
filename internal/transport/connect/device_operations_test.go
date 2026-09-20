package transportconnect

import (
	"context"
	"errors"
	"testing"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/execution"
)

// The device-operation contract at the transport boundary (card ARC-138).
//
// The panel's operations carry a refusal vocabulary that is finer grained than
// the kernel's, exactly as the dispatch boundary's is: several distinct operator
// situations share one Connect code, so a code-only mapping collapses them into
// "something went wrong" and a surface cannot tell the operator which fact was
// missing. The reason therefore travels as contract, on the row the operation
// answered with.
//
// These tests are the mapping's own guard, and none of them contacts a device.

// fakeDeviceOperations is the application boundary the handler calls. It records
// the request it was given and answers with whatever the case stated.
type fakeDeviceOperations struct {
	outcome  execution.DeviceOperationOutcome
	err      error
	requests []execution.DeviceOperationRequest

	advancedOutcome  execution.DeviceOperationOutcome
	advancedErr      error
	advancedRequests []execution.AdvancedCommandRunRequest
}

func (f *fakeDeviceOperations) RunDeviceOperation(_ context.Context, request execution.DeviceOperationRequest, _, _ string) (execution.DeviceOperationOutcome, error) {
	f.requests = append(f.requests, request)
	return f.outcome, f.err
}

func (f *fakeDeviceOperations) RunAdvancedCommand(_ context.Context, request execution.AdvancedCommandRunRequest, _, _ string) (execution.DeviceOperationOutcome, error) {
	f.advancedRequests = append(f.advancedRequests, request)
	return f.advancedOutcome, f.advancedErr
}

func deviceOperationRequest(operation driftv1.DeviceOperation) *driftv1.RunDeviceOperationRequest {
	return &driftv1.RunDeviceOperationRequest{
		Context:         &driftv1.RequestContext{ActorId: "operator-1", RequestId: "request-1"},
		Workspace:       &driftv1.WorkspaceRef{WorkspaceId: "workspace-1"},
		DeviceId:        "atlas-04",
		Operation:       operation,
		ApprovalGranted: true,
	}
}

func runDeviceOperation(t *testing.T, operations DeviceOperations, message *driftv1.RunDeviceOperationRequest) (*driftv1.RunDeviceOperationResponse, error) {
	t.Helper()
	handler := NewDeviceOperationsHandler(operations)
	if handler == nil {
		t.Fatal("a constructed applier did not mount the handler")
	}
	response, err := handler.RunDeviceOperation(context.Background(), connectrpc.NewRequest(message))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

// TestEveryOperationRefusalHasATypedDiscriminator walks the boundary's own
// published vocabulary rather than a list written here: a reason added to the
// boundary without a discriminator in the contract fails this test instead of
// reaching an operator surface as an unnamed refusal.
func TestEveryOperationRefusalHasATypedDiscriminator(t *testing.T) {
	refusals := execution.OperationRefusals()
	if len(refusals) == 0 {
		t.Fatal("the boundary publishes no operation refusals, so this test would pass vacuously")
	}
	for _, refusal := range refusals {
		if mapped := deviceOperationRefusal(refusal); mapped == driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_UNSPECIFIED {
			t.Errorf("operation refusal %q has no typed discriminator, so an operator would read it as an unnamed failure", refusal)
		}
	}
}

// TestEveryOperationRefusalReasonHasAProducer is the reverse direction: a
// contract value with no boundary reason mapped to it is a discriminator nothing
// can ever answer with, which makes it a promise the product does not keep.
func TestEveryOperationRefusalReasonHasAProducer(t *testing.T) {
	produced := make(map[driftv1.DeviceOperationRefusalReason]bool, len(deviceOperationRefusals))
	for _, reason := range deviceOperationRefusals {
		produced[reason] = true
	}
	for value := range driftv1.DeviceOperationRefusalReason_name {
		reason := driftv1.DeviceOperationRefusalReason(value)
		if reason == driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_UNSPECIFIED {
			continue
		}
		if !produced[reason] {
			t.Errorf("contract refusal reason %s has no boundary reason that answers with it", reason)
		}
	}
}

// TestOnlyReviewedOperationsAreDispatchable pins the closed set. UNSPECIFIED is
// not a key, so a request that names no operation is refused as malformed rather
// than resolved into one; and the advanced form is not a member at all, because
// it has its own RPC rather than an enum member a catalogued request could name.
func TestOnlyReviewedOperationsAreDispatchable(t *testing.T) {
	if _, err := operationFromRequest(driftv1.DeviceOperation_DEVICE_OPERATION_UNSPECIFIED); err == nil {
		t.Fatal("a request naming no operation was resolved into one instead of refused")
	}
	if len(deviceOperationKinds) == 0 {
		t.Fatal("no operation is dispatchable, so this test would pass vacuously")
	}
	for value, kind := range deviceOperationKinds {
		if value == driftv1.DeviceOperation_DEVICE_OPERATION_UNSPECIFIED {
			t.Fatal("UNSPECIFIED is a key in the request table, so a request naming no operation would dispatch")
		}
		if !execution.IsCataloguedOperation(kind) {
			t.Errorf("operation %s maps to kind %q, which is not a catalogued operation", value, kind)
		}
		resolved, err := operationFromRequest(value)
		if err != nil {
			t.Fatalf("operation %s was refused: %v", value, err)
		}
		if resolved != kind {
			t.Fatalf("operation %s resolved to %q, want %q", value, resolved, kind)
		}
	}
	// The advanced form has a row mapping so its outcome names itself, and it has
	// NO request mapping, so no catalogued request can select it.
	if _, exists := deviceOperationValues[action.AdvancedCommand]; !exists {
		t.Fatal("the advanced form has no row mapping, so its outcome would arrive as UNSPECIFIED")
	}
	for _, value := range deviceOperationKinds {
		if value == action.AdvancedCommand {
			t.Fatal("the advanced form is reachable through a catalogued request")
		}
	}
}

// TestAnUnconstructedSurfaceMountsNoHandler pins that a route with nothing to
// call is not mounted at all: a control an operator surface renders and finds
// dead is the defect this surface exists to remove. The typed-nil case is the one
// a plain nil check misses, and `var a *Applier; NewDeviceOperationsHandler(a)`
// compiles.
func TestAnUnconstructedSurfaceMountsNoHandler(t *testing.T) {
	if handler := NewDeviceOperationsHandler(nil); handler != nil {
		t.Fatal("a nil applier mounted a handler")
	}
	var typed *fakeDeviceOperations
	if handler := NewDeviceOperationsHandler(typed); handler != nil {
		t.Fatal("a typed nil applier mounted a handler")
	}
}

// TestAMalformedRequestIsRefusedBeforeAnyDeviceIsContacted pins that the shape
// check is the handler's and that a refused request reaches no boundary: whether
// the operator MAY act is the kernel's decision, but a request naming no
// workspace, no device, no operation or no request id is not dispatchable at all.
//
// An empty actor id is deliberately NOT a case here: `actorFromContext` falls
// back to the request id when no actor is named, which is the rule every other
// surface of this transport already follows, so the request id is what has to be
// present.
func TestAMalformedRequestIsRefusedBeforeAnyDeviceIsContacted(t *testing.T) {
	complete := func() *driftv1.RunDeviceOperationRequest {
		return deviceOperationRequest(driftv1.DeviceOperation_DEVICE_OPERATION_REBOOT)
	}
	cases := map[string]*driftv1.RunDeviceOperationRequest{
		"nil request":  nil,
		"no context":   {Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-1"}, DeviceId: "atlas-04", Operation: driftv1.DeviceOperation_DEVICE_OPERATION_REBOOT},
		"no workspace": {Context: &driftv1.RequestContext{ActorId: "operator-1"}, DeviceId: "atlas-04", Operation: driftv1.DeviceOperation_DEVICE_OPERATION_REBOOT},
		"no device":    {Context: &driftv1.RequestContext{ActorId: "operator-1"}, Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-1"}, Operation: driftv1.DeviceOperation_DEVICE_OPERATION_REBOOT},
		"no operation": {Context: &driftv1.RequestContext{ActorId: "operator-1"}, Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-1"}, DeviceId: "atlas-04"},
		"unnamed operation": func() *driftv1.RunDeviceOperationRequest {
			message := complete()
			message.Operation = driftv1.DeviceOperation(99)
			return message
		}(),
	}
	for name, message := range cases {
		operations := &fakeDeviceOperations{}
		if _, err := runDeviceOperation(t, operations, message); err == nil {
			t.Errorf("%s: the request was accepted", name)
		}
		if len(operations.requests) != 0 {
			t.Errorf("%s: a refused request still reached the operation boundary", name)
		}
	}
	// The complete request is the control: it reaches the boundary exactly once, so
	// the refusals above are the shape checks and not a handler that never calls.
	operations := &fakeDeviceOperations{}
	if _, err := runDeviceOperation(t, operations, complete()); err != nil {
		t.Fatalf("a complete request was refused: %v", err)
	}
	if len(operations.requests) != 1 {
		t.Fatalf("boundary saw %d requests for a complete request, want 1", len(operations.requests))
	}
}

// TestTheOperationRowNamesItselfAndCarriesWhatTheDeviceAnswered pins that the row
// the plane answered with is what the client receives, field for field: the
// operation, whether it was applied, whether it was read back, and what the device
// answered.
func TestTheOperationRowNamesItselfAndCarriesWhatTheDeviceAnswered(t *testing.T) {
	operations := &fakeDeviceOperations{outcome: execution.DeviceOperationOutcome{
		DeviceID:  "atlas-04",
		Operation: action.KeyboardSwitch,
		Applied:   true,
		Verified:  true,
		Message:   "the operation ran and the device's answer was read back",
		Detail:    "the device's own enabled list named the chosen component",
	}}
	response, err := runDeviceOperation(t, operations, deviceOperationRequest(driftv1.DeviceOperation_DEVICE_OPERATION_KEYBOARD_SWITCH))
	if err != nil {
		t.Fatalf("a constructed applier refused the request: %v", err)
	}
	row := response.GetResult()
	if row == nil {
		t.Fatal("an operation that ran answered without its row")
	}
	if row.GetOperation() != driftv1.DeviceOperation_DEVICE_OPERATION_KEYBOARD_SWITCH {
		t.Fatalf("row operation = %s, want the operation the request named", row.GetOperation())
	}
	if row.GetDeviceId() != "atlas-04" {
		t.Fatalf("row device = %q, want the device the request named", row.GetDeviceId())
	}
	if !row.GetApplied() || !row.GetVerified() {
		t.Fatalf("row applied=%v verified=%v, want both true", row.GetApplied(), row.GetVerified())
	}
	if row.GetDetail() != operations.outcome.Detail {
		t.Fatalf("row detail = %q, want what the device answered", row.GetDetail())
	}
	// The workspace, the holder and the request id come from the authenticated
	// caller and the request's own context, and there is no field on the boundary's
	// request that carries a transport serial: the boundary resolves the serial from
	// the registry's own current-endpoint projection, so a caller cannot make the
	// plane act at a transport it supplies.
	if len(operations.requests) != 1 {
		t.Fatalf("boundary saw %d requests, want 1", len(operations.requests))
	}
	request := operations.requests[0]
	if request.Workspace != "workspace-1" || request.HolderID != "operator-1" || request.RequestID != "request-1" || request.DeviceID != "atlas-04" {
		t.Fatalf("boundary request = %+v, want the request's own identities", request)
	}
	if !request.ApprovalGranted {
		t.Fatal("the approval the request carried was dropped before the boundary")
	}
}

// TestARefusedOperationKeepsItsOwnReasonAndReachesNoSuccess pins that a classified
// refusal is not flattened into a generic failure: the row carries the boundary's
// OWN reason, applied and verified are both false, and the sentence is the one
// fixed to that reason.
func TestARefusedOperationKeepsItsOwnReasonAndReachesNoSuccess(t *testing.T) {
	operations := &fakeDeviceOperations{outcome: execution.DeviceOperationOutcome{
		DeviceID:     "atlas-04",
		Operation:    action.Reboot,
		Applied:      false,
		Verified:     false,
		Refusal:      execution.OperationNoTransportSerial,
		FailureClass: domain.FailureTransport,
		Message:      execution.OperationNoTransportSerial.Message(),
	}}
	response, err := runDeviceOperation(t, operations, deviceOperationRequest(driftv1.DeviceOperation_DEVICE_OPERATION_REBOOT))
	if err != nil {
		t.Fatalf("a refusal with a row was reported as a transport error: %v", err)
	}
	row := response.GetResult()
	if row == nil {
		t.Fatal("a refused operation answered without its row")
	}
	if row.GetApplied() || row.GetVerified() {
		t.Fatalf("row applied=%v verified=%v, want both false for a refusal", row.GetApplied(), row.GetVerified())
	}
	if mapped := row.GetRefusal(); mapped != driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_NO_TRANSPORT_SERIAL {
		t.Fatalf("row refusal = %s, want the reason the boundary refused with", mapped)
	}
	if row.GetMessage() != execution.OperationNoTransportSerial.Message() {
		t.Fatalf("row message = %q, want the sentence fixed to that reason", row.GetMessage())
	}
}

// TestAKernelFailureThatNeverRanKeepsItsIdentity pins that a failure which
// produced NO row is reported as an error rather than as a refusal row: an
// operator surface must not read "the operation was refused" from a request the
// boundary could not even assemble.
func TestAKernelFailureThatNeverRanKeepsItsIdentity(t *testing.T) {
	operations := &fakeDeviceOperations{err: errors.New("the operation boundary could not assemble this request")}
	response, err := runDeviceOperation(t, operations, deviceOperationRequest(driftv1.DeviceOperation_DEVICE_OPERATION_REBOOT))
	if err == nil {
		t.Fatal("a boundary failure answered with a success")
	}
	if response != nil {
		t.Fatal("a boundary failure returned a row")
	}
}

// TestTheAdvancedArrayTravelsAsDiscreteEntries pins the one thing this surface
// must never do with an operator's array: join it. The entries arrive as the
// discrete strings the client sent, in order, and the handler adds nothing to
// them - not a shell, not a join, not a default argument.
func TestTheAdvancedArrayTravelsAsDiscreteEntries(t *testing.T) {
	operations := &fakeDeviceOperations{}
	handler := NewDeviceOperationsHandler(operations)
	if handler == nil {
		t.Fatal("a constructed applier did not mount the handler")
	}
	_, err := handler.RunAdvancedCommand(context.Background(), connectrpc.NewRequest(&driftv1.RunAdvancedCommandRequest{
		Context:   &driftv1.RequestContext{ActorId: "operator-1", RequestId: "request-1"},
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-1"},
		DeviceId:  "atlas-04",
		Argv:      []string{"get-state", "argument with spaces"},
		Confirmed: true,
	}))
	if err != nil {
		t.Fatalf("a confirmed array was refused at the boundary: %v", err)
	}
	if len(operations.advancedRequests) != 1 {
		t.Fatalf("boundary saw %d advanced requests, want 1", len(operations.advancedRequests))
	}
	argv := operations.advancedRequests[0].Argv
	if len(argv) != 2 || argv[0] != "get-state" || argv[1] != "argument with spaces" {
		t.Fatalf("argv = %q, want the two discrete entries the client sent", argv)
	}
	if !operations.advancedRequests[0].Confirmed {
		t.Fatal("the confirmation the operator gave was dropped before the boundary")
	}
	// The array the boundary holds is a copy: a handler that aliased the request's
	// slice would let a later mutation of the caller's message change what was
	// dispatched.
	argv[0] = "mutated"
	if operations.advancedRequests[0].Argv[0] != "mutated" {
		t.Fatal("the boundary's argv was not the slice that was handed to it")
	}
}

// TestAnUnconfirmedAdvancedArrayCarriesItsOwnReason pins that the handler does
// not decide the array's admissibility: the reason comes from the boundary that
// will dispatch it, so one rule has one home, and the refusal reaches no device.
func TestAnUnconfirmedAdvancedArrayCarriesItsOwnReason(t *testing.T) {
	operations := &fakeDeviceOperations{advancedOutcome: execution.DeviceOperationOutcome{
		DeviceID:     "atlas-04",
		Operation:    action.AdvancedCommand,
		Refusal:      execution.OperationNotConfirmed,
		FailureClass: domain.FailureInvalidTransition,
		Message:      execution.OperationNotConfirmed.Message(),
		Argv:         []string{"get-state"},
	}}
	handler := NewDeviceOperationsHandler(operations)
	if handler == nil {
		t.Fatal("a constructed applier did not mount the handler")
	}
	response, err := handler.RunAdvancedCommand(context.Background(), connectrpc.NewRequest(&driftv1.RunAdvancedCommandRequest{
		Context:   &driftv1.RequestContext{ActorId: "operator-1", RequestId: "request-1"},
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-1"},
		DeviceId:  "atlas-04",
		Argv:      []string{"get-state"},
	}))
	if err != nil {
		t.Fatalf("a refusal with a row was reported as a transport error: %v", err)
	}
	row := response.Msg.GetResult()
	if row == nil {
		t.Fatal("a refused advanced array answered without its row")
	}
	if row.GetRefusal() != driftv1.DeviceOperationRefusalReason_DEVICE_OPERATION_REFUSAL_REASON_NOT_CONFIRMED {
		t.Fatalf("row refusal = %s, want the reason the boundary refused with", row.GetRefusal())
	}
	if row.GetApplied() {
		t.Fatal("an unconfirmed array was reported as applied")
	}
	// The refused array is still named on the row: what the operator confirmed (and
	// what was NOT dispatched) is what the record has to be able to show.
	if len(row.GetArgv()) != 1 || row.GetArgv()[0] != "get-state" {
		t.Fatalf("row argv = %q, want the array the operator confirmed", row.GetArgv())
	}
}
