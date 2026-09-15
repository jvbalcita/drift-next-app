package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/skills"
	store "drift.local/drift-next/internal/store/sqlite"
)

type SkillHandler struct{ db *store.DB }

func NewSkillHandler(db *store.DB) *SkillHandler { return &SkillHandler{db: db} }

func (h *SkillHandler) ListSkills(ctx context.Context, request *connectrpc.Request[driftv1.ListSkillsRequest]) (*connectrpc.Response[driftv1.ListSkillsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list skills request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewSkillRepository(h.db).List(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.Skill, 0, len(page))
	for _, skill := range page {
		out = append(out, skillProto(skill))
	}
	return connectrpc.NewResponse(&driftv1.ListSkillsResponse{Skills: out, Page: pageResponse(next)}), nil
}

func (h *SkillHandler) ListSkillVersions(ctx context.Context, request *connectrpc.Request[driftv1.ListSkillVersionsRequest]) (*connectrpc.Response[driftv1.ListSkillVersionsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list skill versions request is required")
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetSkill())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewSkillRepository(h.db).ListVersions(ctx, workspace, skills.SkillID(id))
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.SkillVersion, 0, len(page))
	for _, version := range page {
		out = append(out, skillVersionProto(version))
	}
	return connectrpc.NewResponse(&driftv1.ListSkillVersionsResponse{Versions: out, Page: pageResponse(next)}), nil
}

func (h *SkillHandler) GetSkillVersion(ctx context.Context, request *connectrpc.Request[driftv1.GetSkillVersionRequest]) (*connectrpc.Response[driftv1.GetSkillVersionResponse], error) {
	if request == nil {
		return nil, invalidArgument("get skill version request is required")
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetVersion())
	if err != nil {
		return nil, err
	}
	version, getErr := store.NewSkillRepository(h.db).GetVersion(ctx, workspace, skills.SkillVersionID(id))
	if getErr != nil {
		return nil, MapError(getErr)
	}
	return connectrpc.NewResponse(&driftv1.GetSkillVersionResponse{Version: skillVersionProto(version)}), nil
}

func (h *SkillHandler) ReviewSkillVersion(ctx context.Context, request *connectrpc.Request[driftv1.ReviewSkillVersionRequest]) (*connectrpc.Response[driftv1.ReviewSkillVersionResponse], error) {
	if request == nil {
		return nil, invalidArgument("review skill version request is required")
	}
	_, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetVersion())
	if err != nil {
		return nil, err
	}
	version, _, reviewErr := store.NewSkillService(h.db).ReviewVersion(ctx, workspace, skills.SkillVersionID(id), actorID, request.Msg.GetReason())
	if reviewErr != nil {
		return nil, MapError(reviewErr)
	}
	return connectrpc.NewResponse(&driftv1.ReviewSkillVersionResponse{Version: skillVersionProto(version)}), nil
}

func (h *SkillHandler) PublishSkillVersion(ctx context.Context, request *connectrpc.Request[driftv1.PublishSkillVersionRequest]) (*connectrpc.Response[driftv1.PublishSkillVersionResponse], error) {
	if request == nil {
		return nil, invalidArgument("publish skill version request is required")
	}
	_, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetVersion())
	if err != nil {
		return nil, err
	}
	version, _, publishErr := store.NewSkillService(h.db).PublishVersion(ctx, workspace, skills.SkillVersionID(id), actorID, request.Msg.GetReason())
	if publishErr != nil {
		return nil, MapError(publishErr)
	}
	return connectrpc.NewResponse(&driftv1.PublishSkillVersionResponse{Version: skillVersionProto(version)}), nil
}

func (h *SkillHandler) ListSharedBrainKnowledge(ctx context.Context, request *connectrpc.Request[driftv1.ListSharedBrainKnowledgeRequest]) (*connectrpc.Response[driftv1.ListSharedBrainKnowledgeResponse], error) {
	if request == nil {
		return nil, invalidArgument("list shared brain knowledge request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewSkillRepository(h.db).ListBrainKnowledge(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.SharedBrainKnowledge, 0, len(page))
	for _, knowledge := range page {
		out = append(out, brainKnowledgeProto(knowledge))
	}
	return connectrpc.NewResponse(&driftv1.ListSharedBrainKnowledgeResponse{Knowledge: out, Page: pageResponse(next)}), nil
}

func skillProto(skill skills.Skill) *driftv1.Skill {
	return &driftv1.Skill{
		Id:          string(skill.ID),
		Workspace:   workspaceRef(skill.Workspace),
		DisplayName: skill.Name,
		State:       skillStateProto(skill.State),
	}
}

func skillVersionProto(version skills.SkillVersion) *driftv1.SkillVersion {
	capabilities := make([]string, 0, len(version.Manifest.RequestedCapabilities))
	for _, capability := range version.Manifest.RequestedCapabilities {
		capabilities = append(capabilities, string(capability))
	}
	return &driftv1.SkillVersion{
		Id:                       string(version.ID),
		SkillId:                  string(version.SkillID),
		Version:                  uint32(version.Version),
		State:                    skillStateProto(version.State),
		TrustState:               skillTrustProto(version.Trust),
		Capabilities:             capabilities,
		SourceRecordingSessionId: string(version.SourceRecordingSession),
		RollbackVersionId:        string(version.RollbackOf),
		CreatedAt:                formatTime(version.CreatedAt),
		ReviewerId:               version.ReviewerID,
		ReviewedAt:               formatTimePtr(version.ReviewedAt),
	}
}

func brainKnowledgeProto(knowledge skills.BrainKnowledge) *driftv1.SharedBrainKnowledge {
	return &driftv1.SharedBrainKnowledge{
		Id:                   knowledge.ID,
		Workspace:            workspaceRef(knowledge.Workspace),
		KnowledgeKey:         knowledge.Key,
		Version:              uint32(knowledge.Version),
		State:                skillStateProto(knowledge.State),
		KnowledgeJson:        knowledge.KnowledgeJSON,
		SourceSkillVersionId: string(knowledge.SourceSkillVersion),
	}
}

func skillStateProto(state skills.State) driftv1.SkillState {
	switch state {
	case skills.Draft:
		return driftv1.SkillState_SKILL_STATE_DRAFT
	case skills.Validated:
		return driftv1.SkillState_SKILL_STATE_VALIDATED
	case skills.Published:
		return driftv1.SkillState_SKILL_STATE_PUBLISHED
	case skills.Deprecated:
		return driftv1.SkillState_SKILL_STATE_DEPRECATED
	case skills.Retired:
		return driftv1.SkillState_SKILL_STATE_RETIRED
	default:
		return driftv1.SkillState_SKILL_STATE_UNSPECIFIED
	}
}

func skillTrustProto(state skills.TrustState) driftv1.TrustState {
	switch state {
	case skills.TrustUnreviewed:
		return driftv1.TrustState_TRUST_STATE_UNREVIEWED
	case skills.TrustReviewed:
		return driftv1.TrustState_TRUST_STATE_REVIEWED
	case skills.TrustApproved:
		return driftv1.TrustState_TRUST_STATE_APPROVED
	case skills.TrustRevoked:
		return driftv1.TrustState_TRUST_STATE_REVOKED
	default:
		return driftv1.TrustState_TRUST_STATE_UNSPECIFIED
	}
}
