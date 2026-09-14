package connection_test

import (
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/edge/connection"
	"drift.local/drift-next/internal/edgeagents"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

func TestFakeRegistryRegistersHeartbeatsAndReportsScopedDevices(t *testing.T) {
	registry := connection.NewFakeRegistry()
	now := time.Date(2026, 9, 14, 3, 0, 0, 0, time.UTC)
	workspace := organizations.WorkspaceID("workspace-a")
	agent, err := registry.RegisterAgent(connection.AgentReport{
		Agent: edgeagents.EdgeAgent{ID: "agent-a", Workspace: workspace, DisplayName: "Fake edge", Version: "fake-1", State: edgeagents.Pending},
		Devices: []connection.DeviceReport{{
			Device:       devices.Device{ID: "device-a", Workspace: workspace, DisplayName: "Fake phone", State: devices.Registered},
			Endpoint:     endpoints.Endpoint{ID: "endpoint-a", Workspace: workspace, DeviceID: "device-a", Serial: "fake-serial", Host: "192.0.2.10", Port: 5555, State: endpoints.Current, ObservedAt: now},
			Capabilities: []action.Capability{action.CapabilityObserve, action.CapabilityTap},
		}},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if agent.State != edgeagents.Active || agent.LastSeenAt == nil {
		t.Fatalf("registered agent = %#v, want active with heartbeat", agent)
	}
	heartbeat, err := registry.Heartbeat(workspace, agent.ID, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.RowVersion != agent.RowVersion+1 {
		t.Fatalf("heartbeat row version = %d, want %d", heartbeat.RowVersion, agent.RowVersion+1)
	}
	devices := registry.ListDevices(workspace)
	if len(devices) != 1 || devices[0].Device.ID != "device-a" || len(devices[0].Capabilities) != 2 {
		t.Fatalf("device reports = %#v, want one scoped fake report", devices)
	}
	if other := registry.ListDevices("workspace-b"); len(other) != 0 {
		t.Fatalf("cross-workspace reports = %#v, want empty", other)
	}
}

func TestFakeRegistryRejectsCrossWorkspaceAndRetiredHeartbeats(t *testing.T) {
	registry := connection.NewFakeRegistry()
	now := time.Date(2026, 9, 14, 3, 0, 0, 0, time.UTC)
	workspace := organizations.WorkspaceID("workspace-a")
	_, err := registry.RegisterAgent(connection.AgentReport{Agent: edgeagents.EdgeAgent{ID: "agent-a", Workspace: workspace, DisplayName: "Fake edge", Version: "fake-1", State: edgeagents.Pending}, Devices: []connection.DeviceReport{{
		Device:       devices.Device{ID: "device-a", Workspace: "workspace-b", DisplayName: "wrong", State: devices.Registered},
		Endpoint:     endpoints.Endpoint{ID: "endpoint-a", Workspace: "workspace-b", DeviceID: "device-a", Serial: "fake", State: endpoints.Current, ObservedAt: now},
		Capabilities: []action.Capability{action.CapabilityObserve},
	}}}, now)
	if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("cross-workspace registration code = %v, want invalid_input", platformerrors.CodeOf(err))
	}
	if _, err := registry.RegisterAgent(connection.AgentReport{Agent: edgeagents.EdgeAgent{ID: "agent-retired", Workspace: workspace, DisplayName: "Retired", Version: "fake-1", State: edgeagents.Retired}}, now); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("retired registration code = %v, want invalid_input", platformerrors.CodeOf(err))
	}
}
