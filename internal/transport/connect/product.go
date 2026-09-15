package transportconnect

import (
	"drift.local/drift-next/internal/discovery"
	store "drift.local/drift-next/internal/store/sqlite"
)

// ProductHandlers is the constructed set of local product Connect adapters.
// Callers mount them explicitly; nothing is registered by reflection.
type ProductHandlers struct {
	Device          *DeviceHandler
	NetworkProfile  *NetworkProfileHandler
	Discovery       *DiscoveryHandler
	Group           *GroupHandler
	Endpoint        *EndpointHandler
	Observation     *ObservationHandler
	Event           *EventHandler
	EdgeAgent       *EdgeAgentHandler
	Lease           *LeaseHandler
	Action          *ActionHandler
	AutomationAgent *AutomationAgentHandler
	Account         *AccountHandler
	Settings        *SettingsHandler
	Policy          *PolicyHandler
	Workflow        *WorkflowHandler
	Run             *RunHandler
	Recording       *RecordingHandler
	Skill           *SkillHandler
	Workspace       *WorkspaceHandler
	Mirror          *MirrorHandler
	Runtime         *RuntimeHandler
}

func NewProductHandlers(db *store.DB, scanner discovery.Scanner) *ProductHandlers {
	if db == nil || scanner == nil {
		return nil
	}
	return &ProductHandlers{
		Device:          NewDeviceHandler(db),
		NetworkProfile:  NewNetworkProfileHandler(db),
		Discovery:       NewDiscoveryHandler(discovery.NewService(db, scanner), db),
		Group:           NewGroupHandler(db),
		Endpoint:        NewEndpointHandler(db),
		Observation:     NewObservationHandler(db),
		Event:           NewEventHandler(db),
		EdgeAgent:       NewEdgeAgentHandler(db),
		Lease:           NewLeaseHandler(db),
		Action:          NewActionHandler(db),
		AutomationAgent: NewAutomationAgentHandler(db),
		Account:         NewAccountHandler(db),
		Settings:        NewSettingsHandler(db),
		Policy:          NewPolicyHandler(db),
		Workflow:        NewWorkflowHandler(db),
		Run:             NewRunHandler(db),
		Recording:       NewRecordingHandler(db),
		Skill:           NewSkillHandler(db),
		Workspace:       NewWorkspaceHandler(db),
		Mirror:          NewMirrorHandler(db),
		Runtime:         NewRuntimeHandler(db, nil, nil),
	}
}
