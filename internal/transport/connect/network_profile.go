package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
)

type NetworkProfileHandler struct{ db *store.DB }

func NewNetworkProfileHandler(db *store.DB) *NetworkProfileHandler {
	return &NetworkProfileHandler{db: db}
}

func (h *NetworkProfileHandler) ListNetworkProfiles(ctx context.Context, request *connectrpc.Request[driftv1.ListNetworkProfilesRequest]) (*connectrpc.Response[driftv1.ListNetworkProfilesResponse], error) {
	if request == nil {
		return nil, invalidArgument("list network profiles request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewNetworkProfileRepository(h.db).List(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.NetworkProfile, 0, len(page))
	for _, profile := range page {
		out = append(out, networkProfileProto(profile))
	}
	return connectrpc.NewResponse(&driftv1.ListNetworkProfilesResponse{Profiles: out, Page: pageResponse(next)}), nil
}

func (h *NetworkProfileHandler) CreateNetworkProfile(ctx context.Context, request *connectrpc.Request[driftv1.CreateNetworkProfileRequest]) (*connectrpc.Response[driftv1.CreateNetworkProfileResponse], error) {
	if request == nil {
		return nil, invalidArgument("create network profile request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	profile, err := networkProfileFromProto(request.Msg.GetProfile())
	if err != nil {
		return nil, err
	}
	if _, err := lookupWorkspace(ctx, h.db, workspaceRef(profile.Workspace)); err != nil {
		return nil, err
	}
	if profile.ID == "" {
		id, idErr := newID(h.db)
		if idErr != nil {
			return nil, idErr
		}
		profile.ID = networkprofiles.NetworkProfileID(id)
	}

	if createErr := store.NewNetworkProfileService(h.db).Create(ctx, profile, actorType, actorID); createErr != nil {
		return nil, MapError(createErr)
	}
	stored, getErr := store.NewNetworkProfileRepository(h.db).Get(ctx, profile.Workspace, profile.ID)
	if getErr != nil {
		return nil, MapError(getErr)
	}
	return connectrpc.NewResponse(&driftv1.CreateNetworkProfileResponse{Profile: networkProfileProto(stored)}), nil
}

func (h *NetworkProfileHandler) UpdateNetworkProfile(ctx context.Context, request *connectrpc.Request[driftv1.UpdateNetworkProfileRequest]) (*connectrpc.Response[driftv1.UpdateNetworkProfileResponse], error) {
	if request == nil {
		return nil, invalidArgument("update network profile request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	profile, err := networkProfileFromProto(request.Msg.GetProfile())
	if err != nil {
		return nil, err
	}
	if _, err := lookupWorkspace(ctx, h.db, workspaceRef(profile.Workspace)); err != nil {
		return nil, err
	}
	if updateErr := store.NewNetworkProfileService(h.db).Update(ctx, profile, actorType, actorID); updateErr != nil {
		return nil, MapError(updateErr)
	}
	stored, getErr := store.NewNetworkProfileRepository(h.db).Get(ctx, profile.Workspace, profile.ID)
	if getErr != nil {
		return nil, MapError(getErr)
	}
	return connectrpc.NewResponse(&driftv1.UpdateNetworkProfileResponse{Profile: networkProfileProto(stored)}), nil
}

// DeleteNetworkProfile removes one saved profile outright. Network profiles
// have no lifecycle, so deletion is the whole operation: scan history that named
// the profile survives with a null reference, and the caller's next list call
// reflects the removal.
func (h *NetworkProfileHandler) DeleteNetworkProfile(ctx context.Context, request *connectrpc.Request[driftv1.DeleteNetworkProfileRequest]) (*connectrpc.Response[driftv1.DeleteNetworkProfileResponse], error) {
	if request == nil {
		return nil, invalidArgument("delete network profile request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	profileID := networkprofiles.NetworkProfileID(request.Msg.GetNetworkProfileId())
	if profileID == "" {
		return nil, invalidArgument("network profile ID is required")
	}
	if deleteErr := store.NewNetworkProfileService(h.db).Delete(ctx, workspace, profileID, actorType, actorID); deleteErr != nil {
		return nil, MapError(deleteErr)
	}
	return connectrpc.NewResponse(&driftv1.DeleteNetworkProfileResponse{}), nil
}

func networkProfileFromProto(msg *driftv1.NetworkProfile) (networkprofiles.NetworkProfile, error) {
	var profile networkprofiles.NetworkProfile
	if msg == nil {
		return profile, invalidArgument("network profile is required")
	}
	if msg.GetWorkspace() == nil || msg.GetWorkspace().GetWorkspaceId() == "" {
		return profile, invalidArgument("workspace ID is required")
	}
	ports := make([]uint16, 0, len(msg.GetAllowedPorts()))
	for _, port := range msg.GetAllowedPorts() {
		if port == 0 || port > 65535 {
			return profile, invalidArgument("allowed ports must be between 1 and 65535")
		}
		ports = append(ports, uint16(port))
	}
	profile = networkprofiles.NetworkProfile{
		ID:            networkprofiles.NetworkProfileID(msg.GetId()),
		Workspace:     organizations.WorkspaceID(msg.GetWorkspace().GetWorkspaceId()),
		Name:          msg.GetDisplayName(),
		AddressPolicy: msg.GetAddressPolicy(),
		Ports:         ports,
		IsDefault:     msg.GetIsDefault(),
	}
	return profile, nil
}

func networkProfileProto(profile networkprofiles.NetworkProfile) *driftv1.NetworkProfile {
	ports := make([]uint32, 0, len(profile.Ports))
	for _, port := range profile.Ports {
		ports = append(ports, uint32(port))
	}
	return &driftv1.NetworkProfile{
		Id:            string(profile.ID),
		Workspace:     workspaceRef(profile.Workspace),
		DisplayName:   profile.Name,
		AddressPolicy: profile.AddressPolicy,
		AllowedPorts:  ports,
		IsDefault:     profile.IsDefault,
	}
}
