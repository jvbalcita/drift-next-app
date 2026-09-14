// Package connection models the control-plane edge boundary with an entirely
// deterministic in-memory fake. It has no sockets, ADB, credentials, or
// device discovery side effects.
package connection

import (
	"strings"
	"sync"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/edgeagents"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type DeviceReport struct {
	Device       devices.Device
	Endpoint     endpoints.Endpoint
	Capabilities []action.Capability
}

type AgentReport struct {
	Agent   edgeagents.EdgeAgent
	Devices []DeviceReport
}

type FakeRegistry struct {
	mu      sync.Mutex
	agents  map[string]edgeagents.EdgeAgent
	devices map[string]DeviceReport
}

func NewFakeRegistry() *FakeRegistry {
	return &FakeRegistry{agents: make(map[string]edgeagents.EdgeAgent), devices: make(map[string]DeviceReport)}
}

func (r *FakeRegistry) RegisterAgent(report AgentReport, now time.Time) (edgeagents.EdgeAgent, error) {
	if r == nil {
		return edgeagents.EdgeAgent{}, platformerrors.New(platformerrors.CodeInvalidInput, "fake registry is required")
	}
	if strings.TrimSpace(string(report.Agent.ID)) == "" || strings.TrimSpace(string(report.Agent.Workspace)) == "" || strings.TrimSpace(report.Agent.DisplayName) == "" || strings.TrimSpace(report.Agent.Version) == "" {
		return edgeagents.EdgeAgent{}, platformerrors.New(platformerrors.CodeInvalidInput, "fake edge-agent identity is required")
	}
	if report.Agent.State != edgeagents.Pending && report.Agent.State != edgeagents.Active {
		return edgeagents.EdgeAgent{}, platformerrors.New(platformerrors.CodeInvalidInput, "fake edge-agent must start pending or active")
	}
	for _, device := range report.Devices {
		if err := validateDeviceReport(report.Agent.Workspace, device); err != nil {
			return edgeagents.EdgeAgent{}, err
		}
	}
	now = now.UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	agent := report.Agent
	agent.State = edgeagents.Active
	agent.LastSeenAt = &now
	agent.RowVersion = 1
	if previous, ok := r.agents[string(agent.ID)]; ok {
		if previous.Workspace != agent.Workspace {
			return edgeagents.EdgeAgent{}, platformerrors.New(platformerrors.CodeConflict, "edge-agent belongs to another workspace")
		}
		agent.RowVersion = previous.RowVersion + 1
	}
	r.agents[string(agent.ID)] = agent
	for _, device := range report.Devices {
		r.devices[deviceKey(agent.Workspace, device.Device.ID)] = cloneDeviceReport(device)
	}
	return cloneAgent(agent), nil
}

func (r *FakeRegistry) Heartbeat(workspace organizations.WorkspaceID, id edgeagents.EdgeAgentID, now time.Time) (edgeagents.EdgeAgent, error) {
	if r == nil {
		return edgeagents.EdgeAgent{}, platformerrors.New(platformerrors.CodeInvalidInput, "fake registry is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	agent, ok := r.agents[string(id)]
	if !ok || agent.Workspace != workspace {
		return edgeagents.EdgeAgent{}, platformerrors.New(platformerrors.CodeNotFound, "fake edge-agent not found")
	}
	if agent.State == edgeagents.Retired {
		return edgeagents.EdgeAgent{}, platformerrors.New(platformerrors.CodeConflict, "retired edge-agent cannot heartbeat")
	}
	if agent.State != edgeagents.Active {
		if err := edgeagents.Transition(agent.State, edgeagents.Active); err != nil {
			return edgeagents.EdgeAgent{}, platformerrors.New(platformerrors.CodeConflict, "edge-agent cannot recover from its current state")
		}
	}
	now = now.UTC()
	agent.State = edgeagents.Active
	agent.LastSeenAt = &now
	agent.RowVersion++
	r.agents[string(id)] = agent
	return cloneAgent(agent), nil
}

func (r *FakeRegistry) ListDevices(workspace organizations.WorkspaceID) []DeviceReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]DeviceReport, 0)
	for _, report := range r.devices {
		if report.Device.Workspace == workspace {
			result = append(result, cloneDeviceReport(report))
		}
	}
	return result
}

func validateDeviceReport(workspace organizations.WorkspaceID, report DeviceReport) error {
	if report.Device.Workspace != workspace || strings.TrimSpace(string(report.Device.ID)) == "" || strings.TrimSpace(report.Device.DisplayName) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "fake device report is invalid")
	}
	if report.Endpoint.Workspace != workspace || report.Endpoint.DeviceID != report.Device.ID || strings.TrimSpace(string(report.Endpoint.ID)) == "" || strings.TrimSpace(report.Endpoint.Serial) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "fake endpoint report is invalid")
	}
	if len(report.Capabilities) == 0 {
		return platformerrors.New(platformerrors.CodeInvalidInput, "fake device capabilities are required")
	}
	return nil
}

func deviceKey(workspace organizations.WorkspaceID, id devices.DeviceID) string {
	return string(workspace) + ":" + string(id)
}

func cloneAgent(agent edgeagents.EdgeAgent) edgeagents.EdgeAgent {
	if agent.LastSeenAt != nil {
		copyTime := *agent.LastSeenAt
		agent.LastSeenAt = &copyTime
	}
	return agent
}

func cloneDeviceReport(report DeviceReport) DeviceReport {
	report.Capabilities = append([]action.Capability(nil), report.Capabilities...)
	return report
}
