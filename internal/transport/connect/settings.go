package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/settings"
	store "drift.local/drift-next/internal/store/sqlite"
)

type SettingsHandler struct{ db *store.DB }

func NewSettingsHandler(db *store.DB) *SettingsHandler { return &SettingsHandler{db: db} }

func (h *SettingsHandler) ListSettings(ctx context.Context, request *connectrpc.Request[driftv1.ListSettingsRequest]) (*connectrpc.Response[driftv1.ListSettingsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list settings request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	scope := settingScopeFromProto(request.Msg.GetScope())
	if scope == "" {
		scope = settings.ScopeWorkspace
	}
	listed, listErr := store.NewSettingRepository(h.db).List(ctx, workspace, scope)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.Setting, 0, len(page))
	for _, setting := range page {
		out = append(out, settingProto(setting))
	}
	return connectrpc.NewResponse(&driftv1.ListSettingsResponse{Settings: out, Page: pageResponse(next)}), nil
}

func (h *SettingsHandler) CreateSetting(ctx context.Context, request *connectrpc.Request[driftv1.CreateSettingRequest]) (*connectrpc.Response[driftv1.CreateSettingResponse], error) {
	if request == nil {
		return nil, invalidArgument("create setting request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	setting, err := settingFromProto(request.Msg.GetSetting())
	if err != nil {
		return nil, err
	}
	if _, err := lookupWorkspace(ctx, h.db, workspaceRef(setting.Workspace)); err != nil {
		return nil, err
	}
	if setting.ID == "" {
		id, idErr := newID(h.db)
		if idErr != nil {
			return nil, idErr
		}
		setting.ID = settings.SettingID(id)
	}
	if setting.State == "" {
		setting.State = settings.Draft
	}
	stored, createErr := store.NewSettingService(h.db).Create(ctx, setting, actorType, actorID)
	if createErr != nil {
		return nil, MapError(createErr)
	}
	return connectrpc.NewResponse(&driftv1.CreateSettingResponse{Setting: settingProto(stored)}), nil
}

func (h *SettingsHandler) UpdateSetting(ctx context.Context, request *connectrpc.Request[driftv1.UpdateSettingRequest]) (*connectrpc.Response[driftv1.UpdateSettingResponse], error) {
	if request == nil {
		return nil, invalidArgument("update setting request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetSetting())
	if err != nil {
		return nil, err
	}
	current, getErr := store.NewSettingRepository(h.db).Get(ctx, workspace, settings.SettingID(id))
	if getErr != nil {
		return nil, MapError(getErr)
	}
	current.ValueJSON = request.Msg.GetValueJson()
	stored, updateErr := store.NewSettingService(h.db).Update(ctx, current, request.Msg.GetExpectedRowVersion(), actorType, actorID)
	if updateErr != nil {
		return nil, MapError(updateErr)
	}
	return connectrpc.NewResponse(&driftv1.UpdateSettingResponse{Setting: settingProto(stored)}), nil
}

func (h *SettingsHandler) TransitionSetting(ctx context.Context, request *connectrpc.Request[driftv1.TransitionSettingRequest]) (*connectrpc.Response[driftv1.TransitionSettingResponse], error) {
	if request == nil {
		return nil, invalidArgument("transition setting request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetSetting())
	if err != nil {
		return nil, err
	}
	stored, transErr := store.NewSettingService(h.db).Transition(ctx, workspace, settings.SettingID(id), settings.State(request.Msg.GetState()), request.Msg.GetExpectedRowVersion(), actorType, actorID)
	if transErr != nil {
		return nil, MapError(transErr)
	}
	return connectrpc.NewResponse(&driftv1.TransitionSettingResponse{Setting: settingProto(stored)}), nil
}

func (h *SettingsHandler) ListSettingHistory(ctx context.Context, request *connectrpc.Request[driftv1.ListSettingHistoryRequest]) (*connectrpc.Response[driftv1.ListSettingHistoryResponse], error) {
	if request == nil {
		return nil, invalidArgument("list setting history request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewSettingRepository(h.db).ListHistory(ctx, workspace, settings.SettingID(request.Msg.GetSettingId()))
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.SettingHistory, 0, len(page))
	for _, change := range page {
		out = append(out, settingHistoryProto(change))
	}
	return connectrpc.NewResponse(&driftv1.ListSettingHistoryResponse{History: out, Page: pageResponse(next)}), nil
}

func settingFromProto(msg *driftv1.Setting) (settings.Setting, error) {
	var setting settings.Setting
	if msg == nil || msg.GetWorkspace() == nil {
		return setting, invalidArgument("setting is required")
	}
	setting = settings.Setting{
		ID:         settings.SettingID(msg.GetId()),
		Workspace:  organizations.WorkspaceID(msg.GetWorkspace().GetWorkspaceId()),
		Scope:      settingScopeFromProto(msg.GetScope()),
		TargetID:   msg.GetTargetId(),
		Key:        msg.GetSettingKey(),
		ValueJSON:  msg.GetValueJson(),
		State:      settings.State(msg.GetState()),
		RowVersion: msg.GetRowVersion(),
	}
	return setting, nil
}

func settingProto(setting settings.Setting) *driftv1.Setting {
	definition := settings.DefinitionFor(setting.Key)
	return &driftv1.Setting{
		Id:          string(setting.ID),
		Workspace:   workspaceRef(setting.Workspace),
		Scope:       settingScopeProto(setting.Scope),
		TargetId:    setting.TargetID,
		SettingKey:  setting.Key,
		ValueJson:   setting.ValueJSON,
		State:       string(setting.State),
		RowVersion:  setting.RowVersion,
		ValueKind:   settingValueKindProto(definition.Kind),
		Risk:        settingRiskProto(definition.Risk),
		CreatedAt:   formatTime(setting.CreatedAt),
		UpdatedAt:   formatTime(setting.UpdatedAt),
	}
}

func settingHistoryProto(change settings.Change) *driftv1.SettingHistory {
	return &driftv1.SettingHistory{
		Id:          change.ID,
		Workspace:   workspaceRef(change.Workspace),
		SettingId:   string(change.SettingID),
		Scope:       settingScopeProto(change.Scope),
		TargetId:    change.TargetID,
		SettingKey:  change.Key,
		ValueJson:   change.ValueJSON,
		State:       string(change.State),
		RowVersion:  change.RowVersion,
		ActorType:   change.ActorType,
		ActorId:     change.ActorID,
		ChangedAt:   formatTime(change.ChangedAt),
	}
}

func settingScopeFromProto(scope driftv1.SettingScope) settings.Scope {
	switch scope {
	case driftv1.SettingScope_SETTING_SCOPE_WORKSPACE:
		return settings.ScopeWorkspace
	case driftv1.SettingScope_SETTING_SCOPE_CONTROL_PLANE:
		return settings.ScopeControlPlane
	case driftv1.SettingScope_SETTING_SCOPE_EDGE_HOST:
		return settings.ScopeEdgeHost
	case driftv1.SettingScope_SETTING_SCOPE_DEVICE:
		return settings.ScopeDevice
	case driftv1.SettingScope_SETTING_SCOPE_AUTOMATION_AGENT:
		return settings.ScopeAutomationAgent
	case driftv1.SettingScope_SETTING_SCOPE_OPERATOR_PREFERENCE:
		return settings.ScopeOperatorPreference
	default:
		return ""
	}
}

func settingScopeProto(scope settings.Scope) driftv1.SettingScope {
	switch scope {
	case settings.ScopeWorkspace:
		return driftv1.SettingScope_SETTING_SCOPE_WORKSPACE
	case settings.ScopeControlPlane:
		return driftv1.SettingScope_SETTING_SCOPE_CONTROL_PLANE
	case settings.ScopeEdgeHost:
		return driftv1.SettingScope_SETTING_SCOPE_EDGE_HOST
	case settings.ScopeDevice:
		return driftv1.SettingScope_SETTING_SCOPE_DEVICE
	case settings.ScopeAutomationAgent:
		return driftv1.SettingScope_SETTING_SCOPE_AUTOMATION_AGENT
	case settings.ScopeOperatorPreference:
		return driftv1.SettingScope_SETTING_SCOPE_OPERATOR_PREFERENCE
	default:
		return driftv1.SettingScope_SETTING_SCOPE_UNSPECIFIED
	}
}

func settingValueKindProto(kind settings.ValueKind) driftv1.SettingValueKind {
	switch kind {
	case settings.ValueBoolean:
		return driftv1.SettingValueKind_SETTING_VALUE_KIND_BOOLEAN
	case settings.ValueInteger:
		return driftv1.SettingValueKind_SETTING_VALUE_KIND_INTEGER
	case settings.ValueEnum:
		return driftv1.SettingValueKind_SETTING_VALUE_KIND_ENUM
	case settings.ValueJSON:
		return driftv1.SettingValueKind_SETTING_VALUE_KIND_JSON
	default:
		return driftv1.SettingValueKind_SETTING_VALUE_KIND_UNSPECIFIED
	}
}

func settingRiskProto(risk settings.RiskClass) driftv1.SettingRisk {
	switch risk {
	case settings.RiskSafetyCritical:
		return driftv1.SettingRisk_SETTING_RISK_SAFETY_CRITICAL
	case settings.RiskLowPreference:
		return driftv1.SettingRisk_SETTING_RISK_LOW_PREFERENCE
	default:
		return driftv1.SettingRisk_SETTING_RISK_UNSPECIFIED
	}
}
