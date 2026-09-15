package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/accounts"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
)

type AccountHandler struct{ db *store.DB }

func NewAccountHandler(db *store.DB) *AccountHandler { return &AccountHandler{db: db} }

func (h *AccountHandler) ListAccountReferences(ctx context.Context, request *connectrpc.Request[driftv1.ListAccountReferencesRequest]) (*connectrpc.Response[driftv1.ListAccountReferencesResponse], error) {
	workspace, offset, limit, err := listPrelude(ctx, h.db, request, request.Msg.GetWorkspace(), request.Msg.GetPage(), "list account references request is required")
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewAccountRepository(h.db).List(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.AccountReference, 0, len(page))
	for _, account := range page {
		out = append(out, accountReferenceProto(account))
	}
	return connectrpc.NewResponse(&driftv1.ListAccountReferencesResponse{Accounts: out, Page: pageResponse(next)}), nil
}

func (h *AccountHandler) ListAccountSources(ctx context.Context, request *connectrpc.Request[driftv1.ListAccountSourcesRequest]) (*connectrpc.Response[driftv1.ListAccountSourcesResponse], error) {
	workspace, offset, limit, err := listPrelude(ctx, h.db, request, request.Msg.GetWorkspace(), request.Msg.GetPage(), "list account sources request is required")
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewAccountRepository(h.db).ListSources(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.AccountSource, 0, len(page))
	for _, source := range page {
		out = append(out, accountSourceProto(source))
	}
	return connectrpc.NewResponse(&driftv1.ListAccountSourcesResponse{Sources: out, Page: pageResponse(next)}), nil
}

func (h *AccountHandler) CreateAccountSource(ctx context.Context, request *connectrpc.Request[driftv1.CreateAccountSourceRequest]) (*connectrpc.Response[driftv1.CreateAccountSourceResponse], error) {
	if request == nil {
		return nil, invalidArgument("create account source request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	source, err := accountSourceFromProto(request.Msg.GetSource())
	if err != nil {
		return nil, err
	}
	if _, err := lookupWorkspace(ctx, h.db, workspaceRef(source.Workspace)); err != nil {
		return nil, err
	}
	if source.ID == "" {
		id, idErr := newID(h.db)
		if idErr != nil {
			return nil, idErr
		}
		source.ID = accounts.AccountSourceID(id)
	}
	if source.State == "" {
		source.State = accounts.SourceActive
	}
	if createErr := store.NewAccountSourceService(h.db).Create(ctx, source, actorType, actorID); createErr != nil {
		return nil, MapError(createErr)
	}
	stored, getErr := store.NewAccountRepository(h.db).GetSource(ctx, source.Workspace, source.ID)
	if getErr != nil {
		return nil, MapError(getErr)
	}
	return connectrpc.NewResponse(&driftv1.CreateAccountSourceResponse{Source: accountSourceProto(stored)}), nil
}

func (h *AccountHandler) UpdateAccountSource(ctx context.Context, request *connectrpc.Request[driftv1.UpdateAccountSourceRequest]) (*connectrpc.Response[driftv1.UpdateAccountSourceResponse], error) {
	if request == nil {
		return nil, invalidArgument("update account source request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetSource())
	if err != nil {
		return nil, err
	}
	current, getErr := store.NewAccountRepository(h.db).GetSource(ctx, workspace, accounts.AccountSourceID(id))
	if getErr != nil {
		return nil, MapError(getErr)
	}
	current.DisplayName = request.Msg.GetDisplayName()
	current.ExternalReference = request.Msg.GetExternalReference()
	current.MetadataJSON = request.Msg.GetMetadataJson()
	if updateErr := store.NewAccountSourceService(h.db).Update(ctx, current, request.Msg.GetExpectedRowVersion(), actorType, actorID); updateErr != nil {
		return nil, MapError(updateErr)
	}
	stored, getErr := store.NewAccountRepository(h.db).GetSource(ctx, workspace, current.ID)
	if getErr != nil {
		return nil, MapError(getErr)
	}
	return connectrpc.NewResponse(&driftv1.UpdateAccountSourceResponse{Source: accountSourceProto(stored)}), nil
}

func (h *AccountHandler) TransitionAccountSource(ctx context.Context, request *connectrpc.Request[driftv1.TransitionAccountSourceRequest]) (*connectrpc.Response[driftv1.TransitionAccountSourceResponse], error) {
	if request == nil {
		return nil, invalidArgument("transition account source request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetSource())
	if err != nil {
		return nil, err
	}
	if transErr := store.NewAccountSourceService(h.db).Transition(ctx, workspace, accounts.AccountSourceID(id), accountSourceStateFromProto(request.Msg.GetState()), request.Msg.GetExpectedRowVersion(), actorType, actorID); transErr != nil {
		return nil, MapError(transErr)
	}
	stored, getErr := store.NewAccountRepository(h.db).GetSource(ctx, workspace, accounts.AccountSourceID(id))
	if getErr != nil {
		return nil, MapError(getErr)
	}
	return connectrpc.NewResponse(&driftv1.TransitionAccountSourceResponse{Source: accountSourceProto(stored)}), nil
}

func (h *AccountHandler) CreateAccount(ctx context.Context, request *connectrpc.Request[driftv1.CreateAccountRequest]) (*connectrpc.Response[driftv1.CreateAccountResponse], error) {
	if request == nil {
		return nil, invalidArgument("create account request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	account, err := accountFromReference(request.Msg.GetAccount())
	if err != nil {
		return nil, err
	}
	if _, err := lookupWorkspace(ctx, h.db, workspaceRef(account.Workspace)); err != nil {
		return nil, err
	}
	if account.ID == "" {
		id, idErr := newID(h.db)
		if idErr != nil {
			return nil, idErr
		}
		account.ID = accounts.AccountID(id)
	}
	stored, createErr := store.NewAccountService(h.db).Create(ctx, account, actorType, actorID)
	if createErr != nil {
		return nil, MapError(createErr)
	}
	return connectrpc.NewResponse(&driftv1.CreateAccountResponse{Account: accountReferenceProto(stored)}), nil
}

func (h *AccountHandler) UpdateAccount(ctx context.Context, request *connectrpc.Request[driftv1.UpdateAccountRequest]) (*connectrpc.Response[driftv1.UpdateAccountResponse], error) {
	if request == nil {
		return nil, invalidArgument("update account request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetAccount())
	if err != nil {
		return nil, err
	}
	current, getErr := store.NewAccountRepository(h.db).Get(ctx, workspace, accounts.AccountID(id))
	if getErr != nil {
		return nil, MapError(getErr)
	}
	current.ExternalRef = request.Msg.GetExternalReference()
	current.Label = request.Msg.GetDisplayName()
	current.MetadataJSON = request.Msg.GetMetadataJson()
	stored, updateErr := store.NewAccountService(h.db).Update(ctx, current, request.Msg.GetExpectedRowVersion(), actorType, actorID)
	if updateErr != nil {
		return nil, MapError(updateErr)
	}
	return connectrpc.NewResponse(&driftv1.UpdateAccountResponse{Account: accountReferenceProto(stored)}), nil
}

func (h *AccountHandler) UpdateAccountState(ctx context.Context, request *connectrpc.Request[driftv1.UpdateAccountStateRequest]) (*connectrpc.Response[driftv1.UpdateAccountStateResponse], error) {
	if request == nil {
		return nil, invalidArgument("update account state request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetAccount())
	if err != nil {
		return nil, err
	}
	stored, transErr := store.NewAccountService(h.db).Transition(ctx, workspace, accounts.AccountID(id), accountStateFromProto(request.Msg.GetState()), request.Msg.GetExpectedRowVersion(), actorType, actorID)
	if transErr != nil {
		return nil, MapError(transErr)
	}
	return connectrpc.NewResponse(&driftv1.UpdateAccountStateResponse{Account: accountReferenceProto(stored)}), nil
}

func (h *AccountHandler) ListAccountServiceStates(ctx context.Context, request *connectrpc.Request[driftv1.ListAccountServiceStatesRequest]) (*connectrpc.Response[driftv1.ListAccountServiceStatesResponse], error) {
	workspace, offset, limit, err := listPrelude(ctx, h.db, request, request.Msg.GetWorkspace(), request.Msg.GetPage(), "list account service states request is required")
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewAccountRepository(h.db).ListServiceStates(ctx, workspace, accounts.AccountID(request.Msg.GetAccountId()))
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.AccountServiceStateProjection, 0, len(page))
	for _, state := range page {
		out = append(out, accountServiceStateProto(state))
	}
	return connectrpc.NewResponse(&driftv1.ListAccountServiceStatesResponse{States: out, Page: pageResponse(next)}), nil
}

func (h *AccountHandler) ListAccountServiceStateHistory(ctx context.Context, request *connectrpc.Request[driftv1.ListAccountServiceStateHistoryRequest]) (*connectrpc.Response[driftv1.ListAccountServiceStateHistoryResponse], error) {
	workspace, offset, limit, err := listPrelude(ctx, h.db, request, request.Msg.GetWorkspace(), request.Msg.GetPage(), "list account service state history request is required")
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewAccountRepository(h.db).ListServiceStateHistory(ctx, workspace, accounts.AccountID(request.Msg.GetAccountId()))
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.AccountServiceStateHistory, 0, len(page))
	for _, history := range page {
		out = append(out, accountServiceStateHistoryProto(history))
	}
	return connectrpc.NewResponse(&driftv1.ListAccountServiceStateHistoryResponse{History: out, Page: pageResponse(next)}), nil
}

func (h *AccountHandler) ListAccountRuns(ctx context.Context, request *connectrpc.Request[driftv1.ListAccountRunsRequest]) (*connectrpc.Response[driftv1.ListAccountRunsResponse], error) {
	workspace, offset, limit, err := listPrelude(ctx, h.db, request, request.Msg.GetWorkspace(), request.Msg.GetPage(), "list account runs request is required")
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewAccountRepository(h.db).ListRuns(ctx, workspace, accounts.AccountID(request.Msg.GetAccountId()))
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.AccountRun, 0, len(page))
	for _, run := range page {
		out = append(out, accountRunProto(run))
	}
	return connectrpc.NewResponse(&driftv1.ListAccountRunsResponse{Runs: out, Page: pageResponse(next)}), nil
}

func (h *AccountHandler) ListAccountRunEvents(ctx context.Context, request *connectrpc.Request[driftv1.ListAccountRunEventsRequest]) (*connectrpc.Response[driftv1.ListAccountRunEventsResponse], error) {
	workspace, offset, limit, err := listPrelude(ctx, h.db, request, request.Msg.GetWorkspace(), request.Msg.GetPage(), "list account run events request is required")
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewAccountRepository(h.db).ListRunEvents(ctx, workspace, accounts.AccountRunID(request.Msg.GetRunId()))
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.AccountRunEvent, 0, len(page))
	for _, event := range page {
		out = append(out, accountRunEventProto(event))
	}
	return connectrpc.NewResponse(&driftv1.ListAccountRunEventsResponse{Events: out, Page: pageResponse(next)}), nil
}

func (h *AccountHandler) AssignAccountDevice(ctx context.Context, request *connectrpc.Request[driftv1.AssignAccountDeviceRequest]) (*connectrpc.Response[driftv1.AssignAccountDeviceResponse], error) {
	if request == nil {
		return nil, invalidArgument("assign account device request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, accountID, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetAccount())
	if err != nil {
		return nil, err
	}
	deviceWorkspace, deviceID, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetDevice())
	if err != nil {
		return nil, err
	}
	if deviceWorkspace != workspace {
		return nil, invalidArgument("account and device must share a workspace")
	}
	id, idErr := newID(h.db)
	if idErr != nil {
		return nil, idErr
	}
	stored, assignErr := store.NewAccountAssignmentService(h.db).Assign(ctx, accounts.AccountDeviceAssignment{
		ID:         accounts.AccountDeviceAssignmentID(id),
		Workspace:  workspace,
		AccountID:  accounts.AccountID(accountID),
		DeviceID:   devices.DeviceID(deviceID),
		State:      accounts.AssignmentActive,
		AssignedAt: h.db.Clock().Now().UTC(),
	}, actorType, actorID)
	if assignErr != nil {
		return nil, MapError(assignErr)
	}
	return connectrpc.NewResponse(&driftv1.AssignAccountDeviceResponse{Assignment: accountAssignmentProto(stored)}), nil
}

func (h *AccountHandler) EndAccountDeviceAssignment(ctx context.Context, request *connectrpc.Request[driftv1.EndAccountDeviceAssignmentRequest]) (*connectrpc.Response[driftv1.EndAccountDeviceAssignmentResponse], error) {
	if request == nil {
		return nil, invalidArgument("end account device assignment request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetAssignment())
	if err != nil {
		return nil, err
	}
	current, getErr := store.NewAccountRepository(h.db).GetAssignment(ctx, workspace, accounts.AccountDeviceAssignmentID(id))
	if getErr != nil {
		return nil, MapError(getErr)
	}
	stored, endErr := store.NewAccountAssignmentService(h.db).End(ctx, workspace, current.ID, current.RowVersion, actorType, actorID)
	if endErr != nil {
		return nil, MapError(endErr)
	}
	return connectrpc.NewResponse(&driftv1.EndAccountDeviceAssignmentResponse{Assignment: accountAssignmentProto(stored)}), nil
}

func (h *AccountHandler) ListAccountDeviceAssignments(ctx context.Context, request *connectrpc.Request[driftv1.ListAccountDeviceAssignmentsRequest]) (*connectrpc.Response[driftv1.ListAccountDeviceAssignmentsResponse], error) {
	workspace, offset, limit, err := listPrelude(ctx, h.db, request, request.Msg.GetWorkspace(), request.Msg.GetPage(), "list account device assignments request is required")
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewAccountRepository(h.db).ListAssignments(ctx, workspace, accounts.AccountID(request.Msg.GetAccountId()))
	if listErr != nil {
		return nil, MapError(listErr)
	}
	if request.Msg.GetDeviceId() != "" {
		filtered := listed[:0]
		for _, assignment := range listed {
			if string(assignment.DeviceID) == request.Msg.GetDeviceId() {
				filtered = append(filtered, assignment)
			}
		}
		listed = filtered
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.AccountDeviceAssignment, 0, len(page))
	for _, assignment := range page {
		out = append(out, accountAssignmentProto(assignment))
	}
	return connectrpc.NewResponse(&driftv1.ListAccountDeviceAssignmentsResponse{Assignments: out, Page: pageResponse(next)}), nil
}

func (h *AccountHandler) ListAccountSyncEvents(ctx context.Context, request *connectrpc.Request[driftv1.ListAccountSyncEventsRequest]) (*connectrpc.Response[driftv1.ListAccountSyncEventsResponse], error) {
	workspace, offset, limit, err := listPrelude(ctx, h.db, request, request.Msg.GetWorkspace(), request.Msg.GetPage(), "list account sync events request is required")
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewAccountRepository(h.db).ListSyncEvents(ctx, workspace, accounts.AccountSourceID(request.Msg.GetSourceId()))
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.AccountSyncEvent, 0, len(page))
	for _, event := range page {
		out = append(out, accountSyncEventProto(event))
	}
	return connectrpc.NewResponse(&driftv1.ListAccountSyncEventsResponse{Events: out, Page: pageResponse(next)}), nil
}

func listPrelude[T any](ctx context.Context, db *store.DB, request *connectrpc.Request[T], workspaceRefMsg *driftv1.WorkspaceRef, page *driftv1.PageRequest, missing string) (organizations.WorkspaceID, int, int, error) {
	if request == nil {
		return "", 0, 0, invalidArgument(missing)
	}
	workspace, err := lookupWorkspace(ctx, db, workspaceRefMsg)
	if err != nil {
		return "", 0, 0, err
	}
	offset, limit, err := parsePage(page)
	if err != nil {
		return "", 0, 0, err
	}
	return workspace, offset, limit, nil
}

func accountSourceFromProto(msg *driftv1.AccountSource) (accounts.AccountSource, error) {
	var source accounts.AccountSource
	if msg == nil || msg.GetWorkspace() == nil {
		return source, invalidArgument("account source is required")
	}
	source = accounts.AccountSource{
		ID:                accounts.AccountSourceID(msg.GetId()),
		Workspace:         organizations.WorkspaceID(msg.GetWorkspace().GetWorkspaceId()),
		Provider:          msg.GetProvider(),
		DisplayName:       msg.GetDisplayName(),
		State:             accountSourceStateFromProto(msg.GetState()),
		ExternalReference: msg.GetExternalReference(),
		MetadataJSON:      msg.GetMetadataJson(),
		RowVersion:        msg.GetRowVersion(),
	}
	return source, nil
}

func accountFromReference(msg *driftv1.AccountReference) (accounts.Account, error) {
	var account accounts.Account
	if msg == nil || msg.GetWorkspace() == nil {
		return account, invalidArgument("account is required")
	}
	account = accounts.Account{
		ID:           accounts.AccountID(msg.GetId()),
		Workspace:    organizations.WorkspaceID(msg.GetWorkspace().GetWorkspaceId()),
		SourceID:     accounts.AccountSourceID(msg.GetSourceId()),
		ExternalRef:  msg.GetExternalReference(),
		Label:        msg.GetDisplayName(),
		MetadataJSON: msg.GetMetadataJson(),
		State:        accountStateFromProto(msg.GetState()),
		RowVersion:   msg.GetRowVersion(),
	}
	return account, nil
}

func accountReferenceProto(account accounts.Account) *driftv1.AccountReference {
	return &driftv1.AccountReference{
		Id:                string(account.ID),
		Workspace:         workspaceRef(account.Workspace),
		SourceId:          string(account.SourceID),
		ExternalReference: account.ExternalRef,
		State:             accountStateProto(account.State),
		DisplayName:       account.Label,
		MetadataJson:      account.MetadataJSON,
		RowVersion:        account.RowVersion,
	}
}

func accountSourceProto(source accounts.AccountSource) *driftv1.AccountSource {
	return &driftv1.AccountSource{
		Id:                string(source.ID),
		Workspace:         workspaceRef(source.Workspace),
		Provider:          source.Provider,
		DisplayName:       source.DisplayName,
		State:             accountSourceStateProto(source.State),
		ExternalReference: source.ExternalReference,
		MetadataJson:      source.MetadataJSON,
		CreatedAt:         formatTime(source.CreatedAt),
		UpdatedAt:         formatTime(source.UpdatedAt),
		RowVersion:        source.RowVersion,
	}
}

func accountStateFromProto(state driftv1.AccountState) accounts.State {
	switch state {
	case driftv1.AccountState_ACCOUNT_STATE_ACTIVE:
		return accounts.Active
	case driftv1.AccountState_ACCOUNT_STATE_SUSPENDED, driftv1.AccountState_ACCOUNT_STATE_INACTIVE:
		return accounts.Inactive
	case driftv1.AccountState_ACCOUNT_STATE_RETIRED:
		return accounts.Retired
	case driftv1.AccountState_ACCOUNT_STATE_DRAFT:
		return accounts.Draft
	default:
		return ""
	}
}

func accountStateProto(state accounts.State) driftv1.AccountState {
	switch state {
	case accounts.Active:
		return driftv1.AccountState_ACCOUNT_STATE_ACTIVE
	case accounts.Inactive:
		return driftv1.AccountState_ACCOUNT_STATE_INACTIVE
	case accounts.Retired:
		return driftv1.AccountState_ACCOUNT_STATE_RETIRED
	case accounts.Draft:
		return driftv1.AccountState_ACCOUNT_STATE_DRAFT
	default:
		return driftv1.AccountState_ACCOUNT_STATE_UNSPECIFIED
	}
}

func accountSourceStateFromProto(state driftv1.AccountSourceState) accounts.SourceState {
	switch state {
	case driftv1.AccountSourceState_ACCOUNT_SOURCE_STATE_ACTIVE:
		return accounts.SourceActive
	case driftv1.AccountSourceState_ACCOUNT_SOURCE_STATE_DISABLED:
		return accounts.SourceDisabled
	case driftv1.AccountSourceState_ACCOUNT_SOURCE_STATE_RETIRED:
		return accounts.SourceRetired
	default:
		return ""
	}
}

func accountSourceStateProto(state accounts.SourceState) driftv1.AccountSourceState {
	switch state {
	case accounts.SourceActive:
		return driftv1.AccountSourceState_ACCOUNT_SOURCE_STATE_ACTIVE
	case accounts.SourceDisabled:
		return driftv1.AccountSourceState_ACCOUNT_SOURCE_STATE_DISABLED
	case accounts.SourceRetired:
		return driftv1.AccountSourceState_ACCOUNT_SOURCE_STATE_RETIRED
	default:
		return driftv1.AccountSourceState_ACCOUNT_SOURCE_STATE_UNSPECIFIED
	}
}

func accountServiceStateProto(state accounts.ServiceStateProjection) *driftv1.AccountServiceStateProjection {
	return &driftv1.AccountServiceStateProjection{
		Id:           string(state.ID),
		Workspace:    workspaceRef(state.Workspace),
		AccountId:    string(state.AccountID),
		ServiceName:  state.ServiceName,
		State:        accountServiceStateEnum(state.State),
		ObservedAt:   formatTime(state.ObservedAt),
		FailureClass: string(state.FailureClass),
		DetailsJson:  state.DetailsJSON,
		RowVersion:   state.RowVersion,
	}
}

func accountServiceStateHistoryProto(history accounts.ServiceStateHistory) *driftv1.AccountServiceStateHistory {
	return &driftv1.AccountServiceStateHistory{
		Id:           string(history.ID),
		Workspace:    workspaceRef(history.Workspace),
		AccountId:    string(history.AccountID),
		ServiceName:  history.ServiceName,
		State:        accountServiceStateEnum(history.State),
		ObservedAt:   formatTime(history.ObservedAt),
		FailureClass: string(history.FailureClass),
		DetailsJson:  history.DetailsJSON,
		RowVersion:   history.RowVersion,
		RecordedAt:   formatTime(history.RecordedAt),
	}
}

func accountServiceStateEnum(state accounts.ServiceState) driftv1.AccountServiceState {
	switch state {
	case accounts.ServiceHealthy:
		return driftv1.AccountServiceState_ACCOUNT_SERVICE_STATE_HEALTHY
	case accounts.ServiceDegraded:
		return driftv1.AccountServiceState_ACCOUNT_SERVICE_STATE_DEGRADED
	case accounts.ServiceFailed:
		return driftv1.AccountServiceState_ACCOUNT_SERVICE_STATE_FAILED
	case accounts.ServiceDisabled:
		return driftv1.AccountServiceState_ACCOUNT_SERVICE_STATE_DISABLED
	default:
		return driftv1.AccountServiceState_ACCOUNT_SERVICE_STATE_UNSPECIFIED
	}
}

func accountRunProto(run accounts.AccountRun) *driftv1.AccountRun {
	return &driftv1.AccountRun{
		Id:            string(run.ID),
		Workspace:     workspaceRef(run.Workspace),
		AccountId:     string(run.AccountID),
		State:         accountRunStateProto(run.State),
		RequestedAt:   formatTime(run.RequestedAt),
		StartedAt:     formatTimePtr(run.StartedAt),
		FinishedAt:    formatTimePtr(run.FinishedAt),
		FailureClass:  string(run.FailureClass),
		CorrelationId: run.CorrelationID,
		RowVersion:    run.RowVersion,
	}
}

func accountRunStateProto(state accounts.AccountRunState) driftv1.AccountRunState {
	switch state {
	case accounts.RunRequested:
		return driftv1.AccountRunState_ACCOUNT_RUN_STATE_REQUESTED
	case accounts.RunRunning:
		return driftv1.AccountRunState_ACCOUNT_RUN_STATE_RUNNING
	case accounts.RunCompleted:
		return driftv1.AccountRunState_ACCOUNT_RUN_STATE_COMPLETED
	case accounts.RunFailed:
		return driftv1.AccountRunState_ACCOUNT_RUN_STATE_FAILED
	case accounts.RunCancelled:
		return driftv1.AccountRunState_ACCOUNT_RUN_STATE_CANCELLED
	default:
		return driftv1.AccountRunState_ACCOUNT_RUN_STATE_UNSPECIFIED
	}
}

func accountRunEventProto(event accounts.AccountRunEvent) *driftv1.AccountRunEvent {
	return &driftv1.AccountRunEvent{
		Id:            string(event.ID),
		Workspace:     workspaceRef(event.Workspace),
		RunId:         string(event.RunID),
		State:         accountRunStateProto(event.State),
		FailureClass:  string(event.FailureClass),
		OccurredAt:    formatTime(event.OccurredAt),
		ActorType:     event.ActorType,
		ActorId:       event.ActorID,
		CorrelationId: event.CorrelationID,
	}
}

func accountAssignmentProto(assignment accounts.AccountDeviceAssignment) *driftv1.AccountDeviceAssignment {
	state := driftv1.AccountAssignmentState_ACCOUNT_ASSIGNMENT_STATE_UNSPECIFIED
	switch assignment.State {
	case accounts.AssignmentActive:
		state = driftv1.AccountAssignmentState_ACCOUNT_ASSIGNMENT_STATE_ACTIVE
	case accounts.AssignmentEnded:
		state = driftv1.AccountAssignmentState_ACCOUNT_ASSIGNMENT_STATE_ENDED
	}
	return &driftv1.AccountDeviceAssignment{
		Id:         string(assignment.ID),
		Workspace:  workspaceRef(assignment.Workspace),
		AccountId:  string(assignment.AccountID),
		DeviceId:   string(assignment.DeviceID),
		State:      state,
		AssignedAt: formatTime(assignment.AssignedAt),
		EndedAt:    formatTimePtr(assignment.EndedAt),
		RowVersion: assignment.RowVersion,
	}
}

func accountSyncEventProto(event accounts.SyncEvent) *driftv1.AccountSyncEvent {
	outcome := driftv1.AccountSyncOutcome_ACCOUNT_SYNC_OUTCOME_UNSPECIFIED
	switch event.Outcome {
	case accounts.SyncAccepted:
		outcome = driftv1.AccountSyncOutcome_ACCOUNT_SYNC_OUTCOME_ACCEPTED
	case accounts.SyncRejected:
		outcome = driftv1.AccountSyncOutcome_ACCOUNT_SYNC_OUTCOME_REJECTED
	case accounts.SyncFailed:
		outcome = driftv1.AccountSyncOutcome_ACCOUNT_SYNC_OUTCOME_FAILED
	case accounts.SyncDisabled:
		outcome = driftv1.AccountSyncOutcome_ACCOUNT_SYNC_OUTCOME_DISABLED
	}
	accountID := ""
	if event.AccountID != nil {
		accountID = string(*event.AccountID)
	}
	return &driftv1.AccountSyncEvent{
		Id:             string(event.ID),
		Workspace:      workspaceRef(event.Workspace),
		SourceId:       string(event.SourceID),
		AccountId:      accountID,
		Outcome:        outcome,
		IdempotencyKey: event.IdempotencyKey,
		CorrelationId:  event.CorrelationID,
		OccurredAt:     formatTime(event.OccurredAt),
		DetailsJson:    event.DetailsJSON,
		EventName:      event.EventName,
	}
}
