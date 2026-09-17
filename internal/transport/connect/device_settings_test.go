package transportconnect_test

import (
	"context"
	"strings"
	"testing"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/execution"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// fakeDeviceSettings is the application boundary the handler calls. It records
// the request it was given and answers with whatever the case stated.
type fakeDeviceSettings struct {
	report   execution.SettingsApplyReport
	err      error
	requests []execution.SettingsApplyRequest
}

func (f *fakeDeviceSettings) ApplyDeviceSettings(_ context.Context, request execution.SettingsApplyRequest, _, _ string) (execution.SettingsApplyReport, error) {
	f.requests = append(f.requests, request)
	return f.report, f.err
}

func applyDeviceSettings(t *testing.T, settings transportconnect.DeviceSettings, message *driftv1.ApplyDeviceSettingsRequest) (*driftv1.ApplyDeviceSettingsResponse, error) {
	t.Helper()
	handler := transportconnect.NewDeviceSettingsHandler(settings)
	if handler == nil {
		t.Fatal("a constructed applier did not mount the handler")
	}
	response, err := handler.ApplyDeviceSettings(context.Background(), connectrpc.NewRequest(message))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func applySettingsRequest(requestID string, settings ...driftv1.DeviceSetting) *driftv1.ApplyDeviceSettingsRequest {
	return &driftv1.ApplyDeviceSettingsRequest{
		Context:   requestContext(requestID),
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		Settings:  settings,
	}
}

// TestDeviceSettingsContractMapsEveryReviewedSettingAndRefusal is the separation
// assertion for this surface: the contract's closed enum and the apply boundary's
// refusal vocabulary are bound one to one, and neither side has a member the
// other does not know. It walks both vocabularies in full rather than the members
// someone remembered to list.
func TestDeviceSettingsContractMapsEveryReviewedSettingAndRefusal(t *testing.T) {
	// Every reviewed setting the boundary runs is named by a contract value, and
	// the two sets have the same size.
	settings := execution.ReviewedSettings()
	if len(settings) != 2 {
		t.Fatalf("the boundary runs %d reviewed settings (%v), want the two this surface publishes", len(settings), settings)
	}
	named := map[driftv1.DeviceSetting]int{}
	for _, kind := range settings {
		message := applySettingsRequest("mapping", deviceSettingFor(kind))
		if message.GetSettings()[0] == driftv1.DeviceSetting_DEVICE_SETTING_UNSPECIFIED {
			t.Fatalf("the reviewed setting %q has no contract value", kind)
		}
		named[message.GetSettings()[0]]++
	}
	for value, count := range named {
		if count != 1 {
			t.Fatalf("contract value %v is bound to %d reviewed settings, want 1", value, count)
		}
	}

	// Every member of the contract's enum is either the unspecified zero the
	// surface refuses, or a setting the boundary actually runs.
	enum := driftv1.DeviceSetting(0).Descriptor().Values()
	published := 0
	for index := 0; index < enum.Len(); index++ {
		value := driftv1.DeviceSetting(enum.Get(index).Number())
		if value == driftv1.DeviceSetting_DEVICE_SETTING_UNSPECIFIED {
			continue
		}
		published++
		if _, ok := named[value]; !ok {
			t.Fatalf("the contract publishes %v, which no reviewed setting maps to", value)
		}
	}
	if published != len(settings) {
		t.Fatalf("the contract publishes %d settings, the boundary runs %d", published, len(settings))
	}

	// Every refusal the boundary publishes has its own contract value, and no
	// contract value is left without a producer.
	refusals := execution.SettingsRefusals()
	if len(refusals) == 0 {
		t.Fatal("the boundary publishes no refusals, so the assertions below are vacuous")
	}
	if _, err := applyDeviceSettings(t, &fakeDeviceSettings{report: execution.SettingsApplyReport{Results: refusalRowsForTest(refusals, domain.FailurePostcondition)}}, applySettingsRequest("mapping-refusals", driftv1.DeviceSetting_DEVICE_SETTING_ROTATION_LOCK)); err != nil {
		t.Fatalf("render refusals: %v", err)
	}
	rendered := map[driftv1.DeviceSettingRefusalReason]execution.SettingsRefusal{}
	response, err := applyDeviceSettings(t, &fakeDeviceSettings{report: execution.SettingsApplyReport{Results: refusalRowsForTest(refusals, domain.FailurePostcondition)}}, applySettingsRequest("mapping-refusals", driftv1.DeviceSetting_DEVICE_SETTING_ROTATION_LOCK))
	if err != nil {
		t.Fatalf("render refusals: %v", err)
	}
	for _, row := range response.GetResults() {
		if row.GetRefusal() == driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_UNSPECIFIED {
			t.Fatalf("refusal %q rendered as UNSPECIFIED", row.GetMessage())
		}
		if _, clash := rendered[row.GetRefusal()]; clash {
			t.Fatalf("two refusals render as the same contract value: %v", row.GetRefusal())
		}
		rendered[row.GetRefusal()] = execution.SettingsRefusal(row.GetDeviceId())
	}
	reasons := driftv1.DeviceSettingRefusalReason(0).Descriptor().Values()
	declared := 0
	for index := 0; index < reasons.Len(); index++ {
		value := driftv1.DeviceSettingRefusalReason(reasons.Get(index).Number())
		if value == driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_UNSPECIFIED {
			continue
		}
		declared++
		if _, ok := rendered[value]; !ok {
			t.Fatalf("the contract declares refusal %v, which the boundary never produces", value)
		}
	}
	if declared != len(refusals) {
		t.Fatalf("the contract declares %d refusals, the boundary publishes %d", declared, len(refusals))
	}
}

// refusalRowsForTest renders one row per published refusal, carrying the refusal
// itself in the message so the mapping can be read back off the contract value.
func refusalRowsForTest(refusals []execution.SettingsRefusal, failure domain.FailureClass) []execution.DeviceSettingOutcome {
	rows := make([]execution.DeviceSettingOutcome, 0, len(refusals))
	for _, refusal := range refusals {
		rows = append(rows, execution.DeviceSettingOutcome{
			DeviceID: string(refusal), Setting: action.RotationLock, Refusal: refusal, FailureClass: failure,
		})
	}
	return rows
}

// deviceSettingFor maps a reviewed kind to the contract value the surface
// publishes for it, through the handler itself rather than through an exported
// table: a request naming the value must reach the boundary as that kind.
func deviceSettingFor(kind action.Kind) driftv1.DeviceSetting {
	switch kind {
	case action.RotationLock:
		return driftv1.DeviceSetting_DEVICE_SETTING_ROTATION_LOCK
	case action.AutofillOff:
		return driftv1.DeviceSetting_DEVICE_SETTING_AUTOFILL_OFF
	default:
		return driftv1.DeviceSetting_DEVICE_SETTING_UNSPECIFIED
	}
}

// TestApplyDeviceSettingsRefusesAShapeErrorBeforeTheBoundaryIsAsked: the handler
// validates only the request's SHAPE. Authority — the lease, the policy decision,
// the emergency stop and approval — belongs to the kernel, so the handler must not
// answer those questions here.
func TestApplyDeviceSettingsRefusesAShapeErrorBeforeTheBoundaryIsAsked(t *testing.T) {
	cases := map[string]*driftv1.ApplyDeviceSettingsRequest{
		"no workspace": {
			Context:  requestContext("shape-1"),
			Settings: []driftv1.DeviceSetting{driftv1.DeviceSetting_DEVICE_SETTING_ROTATION_LOCK},
		},
		"no setting": {
			Context:   requestContext("shape-2"),
			Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		},
		"an unspecified setting": applySettingsRequest("shape-3", driftv1.DeviceSetting_DEVICE_SETTING_UNSPECIFIED),
		"the same setting twice": applySettingsRequest("shape-4",
			driftv1.DeviceSetting_DEVICE_SETTING_ROTATION_LOCK,
			driftv1.DeviceSetting_DEVICE_SETTING_ROTATION_LOCK),
		"a setting outside the enum": applySettingsRequest("shape-5", driftv1.DeviceSetting(99)),
	}
	for name, message := range cases {
		t.Run(name, func(t *testing.T) {
			applier := &fakeDeviceSettings{}
			_, err := applyDeviceSettings(t, applier, message)
			if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
				t.Fatalf("code = %v err = %v, want invalid_argument", connectrpc.CodeOf(err), err)
			}
			if len(applier.requests) != 0 {
				t.Fatal("a shape error reached the application boundary")
			}
		})
	}
}

// TestApplyDeviceSettingsCarriesTheRequestThroughUnchanged: the boundary receives
// the workspace, the actor and the settings the caller named, and nothing the
// handler invented.
func TestApplyDeviceSettingsCarriesTheRequestThroughUnchanged(t *testing.T) {
	applier := &fakeDeviceSettings{}
	message := applySettingsRequest("carry-1",
		driftv1.DeviceSetting_DEVICE_SETTING_AUTOFILL_OFF,
		driftv1.DeviceSetting_DEVICE_SETTING_ROTATION_LOCK)
	message.ApprovalGranted = true
	if _, err := applyDeviceSettings(t, applier, message); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(applier.requests) != 1 {
		t.Fatalf("boundary calls = %d, want 1", len(applier.requests))
	}
	request := applier.requests[0]
	if request.Workspace != "workspace-a" || request.RequestID != "carry-1" || !request.ApprovalGranted {
		t.Fatalf("boundary request = %#v, want the caller's own workspace, request id and approval", request)
	}
	if len(request.Settings) != 2 || request.Settings[0] != action.AutofillOff || request.Settings[1] != action.RotationLock {
		t.Fatalf("boundary settings = %v, want the settings the caller named in the order it named them", request.Settings)
	}
}

// TestApplyDeviceSettingsResponseReportsEveryRowAndTheCounts: a client renders
// the rows, and the counts never stand in for them.
func TestApplyDeviceSettingsResponseReportsEveryRowAndTheCounts(t *testing.T) {
	report := execution.SettingsApplyReport{
		TotalDevices:   2,
		AppliedDevices: 1,
		FailedDevices:  1,
		Results: []execution.DeviceSettingOutcome{
			{DeviceID: "device-alpha", Setting: action.RotationLock, Applied: true, Verified: true, Message: "applied"},
			{DeviceID: "device-alpha", Setting: action.AutofillOff, Applied: true, Verified: true, Message: "applied"},
			{DeviceID: "device-beta", Setting: action.RotationLock, Refusal: execution.SettingsPostconditionFailed, Verified: true, FailureClass: domain.FailurePostcondition, Message: execution.SettingsPostconditionFailed.Message()},
		},
	}
	response, err := applyDeviceSettings(t, &fakeDeviceSettings{report: report}, applySettingsRequest("rows-1", driftv1.DeviceSetting_DEVICE_SETTING_ROTATION_LOCK))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if response.GetTotalDevices() != 2 || response.GetAppliedDevices() != 1 || response.GetFailedDevices() != 1 {
		t.Fatalf("counts = %d/%d/%d, want 2/1/1", response.GetTotalDevices(), response.GetAppliedDevices(), response.GetFailedDevices())
	}
	if len(response.GetResults()) != 3 {
		t.Fatalf("rows = %d, want every row the boundary reported", len(response.GetResults()))
	}
	failed := response.GetResults()[2]
	if failed.GetDeviceId() != "device-beta" || failed.GetApplied() || !failed.GetVerified() {
		t.Fatalf("the failed row = %#v, want the device named and the read-back reported", failed)
	}
	if failed.GetRefusal() != driftv1.DeviceSettingRefusalReason_DEVICE_SETTING_REFUSAL_REASON_POSTCONDITION_FAILED {
		t.Fatalf("failed row refusal = %v, want postcondition_failed", failed.GetRefusal())
	}
	if !strings.Contains(failed.GetMessage(), "does not report") {
		t.Fatalf("failed row message = %q, want the boundary's fixed sentence", failed.GetMessage())
	}
}

// TestDeviceSettingsHandlerIsAbsentWithoutAnApplier: a route whose applier cannot
// apply is worse than no route, because it advertises a surface that cannot work.
func TestDeviceSettingsHandlerIsAbsentWithoutAnApplier(t *testing.T) {
	if handler := transportconnect.NewDeviceSettingsHandler(nil); handler != nil {
		t.Fatal("a handler was constructed with no applier")
	}
	var typedNil *fakeDeviceSettings
	if handler := transportconnect.NewDeviceSettingsHandler(typedNil); handler != nil {
		t.Fatal("a handler was constructed around a typed-nil applier")
	}
	handler := transportconnect.NewDeviceSettingsHandler(&fakeDeviceSettings{})
	if handler == nil {
		t.Fatal("a constructed applier did not mount the handler")
	}
	if _, err := handler.ApplyDeviceSettings(context.Background(), connectrpc.NewRequest(applySettingsRequest("absent-1", driftv1.DeviceSetting_DEVICE_SETTING_ROTATION_LOCK))); err != nil {
		t.Fatalf("a constructed handler refused a valid request: %v", err)
	}
	var absent *transportconnect.DeviceSettingsHandler
	if _, err := absent.ApplyDeviceSettings(context.Background(), connectrpc.NewRequest(applySettingsRequest("absent-2"))); connectrpc.CodeOf(err) != connectrpc.CodeUnavailable {
		t.Fatalf("an unconstructed handler answered %v, want unavailable", connectrpc.CodeOf(err))
	}
}
