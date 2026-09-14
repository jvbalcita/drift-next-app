package sqlite

import (
	"context"

	"drift.local/drift-next/internal/artifacts"
	"drift.local/drift-next/internal/assignments"
	"drift.local/drift-next/internal/audit"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/edgeagents"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/events"
	"drift.local/drift-next/internal/groups"
	"drift.local/drift-next/internal/health"
	"drift.local/drift-next/internal/inventory"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/observations"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/outbox"
)

// Typed read ports keep domain callers independent of SQL and prevent a
// generic table-CRUD escape hatch.
type WorkspaceReader interface {
	Get(context.Context, organizations.WorkspaceID) (organizations.Workspace, error)
	List(context.Context) ([]organizations.Workspace, error)
}
type EdgeAgentReader interface {
	Get(context.Context, organizations.WorkspaceID, edgeagents.EdgeAgentID) (edgeagents.EdgeAgent, error)
	List(context.Context, organizations.WorkspaceID) ([]edgeagents.EdgeAgent, error)
}
type DeviceReader interface {
	Get(context.Context, organizations.WorkspaceID, devices.DeviceID) (devices.Device, error)
	List(context.Context, organizations.WorkspaceID) ([]devices.Device, error)
}
type EndpointReader interface {
	ListCurrent(context.Context, organizations.WorkspaceID, devices.DeviceID) ([]endpoints.Endpoint, error)
}
type NetworkProfileReader interface {
	List(context.Context, organizations.WorkspaceID) ([]networkprofiles.NetworkProfile, error)
}
type DiscoveryReader interface {
	ListCandidates(context.Context, organizations.WorkspaceID, discovery.CandidateState) ([]discovery.ScanCandidate, error)
}
type GroupReader interface {
	List(context.Context, organizations.WorkspaceID) ([]groups.Group, error)
	ListMemberships(context.Context, organizations.WorkspaceID, groups.GroupID) ([]groups.Membership, error)
}
type AssignmentReader interface {
	ListBindings(context.Context, organizations.WorkspaceID, devices.DeviceID) ([]assignments.EdgeBinding, error)
	ListAssignments(context.Context, organizations.WorkspaceID, devices.DeviceID) ([]assignments.AutomationAssignment, error)
}
type ObservationReader interface {
	Get(context.Context, organizations.WorkspaceID, observations.ObservationID) (observations.ObservationSnapshot, error)
	List(context.Context, organizations.WorkspaceID, string) ([]observations.ObservationSnapshot, error)
	Current(context.Context, organizations.WorkspaceID, string) (observations.ObservationSnapshot, error)
}
type InventoryReader interface {
	Current(context.Context, organizations.WorkspaceID, string) (inventory.Record, error)
	ListSnapshots(context.Context, organizations.WorkspaceID, string) ([]inventory.Snapshot, error)
}
type HealthReader interface {
	Current(context.Context, organizations.WorkspaceID, string) (health.Current, error)
	List(context.Context, organizations.WorkspaceID, string) ([]health.Sample, error)
}
type ArtifactReader interface {
	Get(context.Context, organizations.WorkspaceID, artifacts.ArtifactID) (artifacts.Artifact, error)
	ListReferences(context.Context, organizations.WorkspaceID, string, string) ([]artifacts.Reference, error)
}
type EventReader interface {
	List(context.Context, organizations.WorkspaceID, string) ([]events.Event, error)
	ListDevice(context.Context, organizations.WorkspaceID, string, string) ([]events.Event, error)
}
type AuditReader interface {
	List(context.Context, string, string) ([]audit.Event, error)
}
type OutboxReader interface {
	ListPending(context.Context, organizations.WorkspaceID, int) ([]outbox.Message, error)
	MarkDelivered(context.Context, organizations.WorkspaceID, outbox.MessageID, int) error
}
