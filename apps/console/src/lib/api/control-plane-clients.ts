import { create } from "@bufbuild/protobuf"
import type { DescMessage, MessageInitShape, MessageShape } from "@bufbuild/protobuf"
import {
  AssignAccountDeviceRequestSchema,
  AssignAccountDeviceResponseSchema,
  CreateAccountRequestSchema,
  CreateAccountResponseSchema,
  CreateAccountSourceRequestSchema,
  CreateAccountSourceResponseSchema,
  EndAccountDeviceAssignmentRequestSchema,
  EndAccountDeviceAssignmentResponseSchema,
  ListAccountDeviceAssignmentsRequestSchema,
  ListAccountDeviceAssignmentsResponseSchema,
  ListAccountReferencesRequestSchema,
  ListAccountReferencesResponseSchema,
  ListAccountRunEventsRequestSchema,
  ListAccountRunEventsResponseSchema,
  ListAccountRunsRequestSchema,
  ListAccountRunsResponseSchema,
  ListAccountServiceStateHistoryRequestSchema,
  ListAccountServiceStateHistoryResponseSchema,
  ListAccountServiceStatesRequestSchema,
  ListAccountServiceStatesResponseSchema,
  ListAccountSourcesRequestSchema,
  ListAccountSourcesResponseSchema,
  ListAccountSyncEventsRequestSchema,
  ListAccountSyncEventsResponseSchema,
  TransitionAccountSourceRequestSchema,
  TransitionAccountSourceResponseSchema,
  UpdateAccountRequestSchema,
  UpdateAccountResponseSchema,
  UpdateAccountSourceRequestSchema,
  UpdateAccountSourceResponseSchema,
  UpdateAccountStateRequestSchema,
  UpdateAccountStateResponseSchema,
  type AccountSourceState,
  type AccountState,
} from "@/gen/drift/v1/account_pb"
import { SubmitActionRequestSchema, SubmitActionResponseSchema } from "@/gen/drift/v1/action_pb"
import {
  DeleteArtifactRequestSchema,
  DeleteArtifactResponseSchema,
  GetStorageHealthRequestSchema,
  GetStorageHealthResponseSchema,
  ListArtifactsRequestSchema,
  ListArtifactsResponseSchema,
  ReadArtifactRequestSchema,
  ReadArtifactResponseSchema,
} from "@/gen/drift/v1/artifact_pb"
import {
  ListAutomationAgentsRequestSchema,
  ListAutomationAgentsResponseSchema,
} from "@/gen/drift/v1/automation_agent_pb"
import { PageRequestSchema, ResourceRefSchema } from "@/gen/drift/v1/common_pb"
import {
  GetDeviceRequestSchema,
  GetDeviceResponseSchema,
  ListDevicesRequestSchema,
  ListDevicesResponseSchema,
} from "@/gen/drift/v1/device_pb"
import {
  DecideScanCandidateRequestSchema,
  DecideScanCandidateResponseSchema,
  ListScanCandidatesRequestSchema,
  ListScanCandidatesResponseSchema,
  ListScanRunsRequestSchema,
  ListScanRunsResponseSchema,
  RegisterScanCandidateRequestSchema,
  RegisterScanCandidateResponseSchema,
  StartScanRequestSchema,
  StartScanResponseSchema,
} from "@/gen/drift/v1/discovery_pb"
import {
  GetEdgeAgentRequestSchema,
  GetEdgeAgentResponseSchema,
  ListEdgeAgentsRequestSchema,
  ListEdgeAgentsResponseSchema,
} from "@/gen/drift/v1/edge_agent_pb"
import {
  ListDeviceEndpointsRequestSchema,
  ListDeviceEndpointsResponseSchema,
} from "@/gen/drift/v1/endpoint_pb"
import {
  ListAuditEventsRequestSchema,
  ListAuditEventsResponseSchema,
  ListOperationalEventsRequestSchema,
  ListOperationalEventsResponseSchema,
} from "@/gen/drift/v1/event_pb"
import {
  ListDeviceGroupsRequestSchema,
  ListDeviceGroupsResponseSchema,
  MoveDeviceToGroupRequestSchema,
  MoveDeviceToGroupResponseSchema,
} from "@/gen/drift/v1/group_pb"
import {
  AcquireDeviceLeaseRequestSchema,
  AcquireDeviceLeaseResponseSchema,
  CloseControlSessionRequestSchema,
  CloseControlSessionResponseSchema,
  ListControlSessionsRequestSchema,
  ListControlSessionsResponseSchema,
  ListDeviceLeasesRequestSchema,
  ListDeviceLeasesResponseSchema,
  OpenControlSessionRequestSchema,
  OpenControlSessionResponseSchema,
  ReleaseDeviceLeaseRequestSchema,
  ReleaseDeviceLeaseResponseSchema,
  RenewDeviceLeaseRequestSchema,
  RenewDeviceLeaseResponseSchema,
} from "@/gen/drift/v1/lease_pb"
import {
  CreateNetworkProfileRequestSchema,
  CreateNetworkProfileResponseSchema,
  ListNetworkProfilesRequestSchema,
  ListNetworkProfilesResponseSchema,
  UpdateNetworkProfileRequestSchema,
  UpdateNetworkProfileResponseSchema,
  type NetworkProfile,
} from "@/gen/drift/v1/network_profile_pb"
import {
  GetObservationSnapshotRequestSchema,
  GetObservationSnapshotResponseSchema,
  ListObservationSnapshotsRequestSchema,
  ListObservationSnapshotsResponseSchema,
} from "@/gen/drift/v1/observation_pb"
import {
  GetWorkspaceRequestSchema,
  GetWorkspaceResponseSchema,
  ListWorkspacesRequestSchema,
  ListWorkspacesResponseSchema,
} from "@/gen/drift/v1/organization_pb"
import {
  ActivatePolicyRequestSchema,
  ActivatePolicyResponseSchema,
  CreatePolicyVersionRequestSchema,
  CreatePolicyVersionResponseSchema,
  ListPoliciesRequestSchema,
  ListPoliciesResponseSchema,
  ListPolicyDecisionsRequestSchema,
  ListPolicyDecisionsResponseSchema,
  RetirePolicyRequestSchema,
  RetirePolicyResponseSchema,
} from "@/gen/drift/v1/policy_pb"
import {
  ListRecordingSessionsRequestSchema,
  ListRecordingSessionsResponseSchema,
} from "@/gen/drift/v1/recording_pb"
import {
  CancelWorkflowRunRequestSchema,
  CancelWorkflowRunResponseSchema,
  ListRunTargetsRequestSchema,
  ListRunTargetsResponseSchema,
  ListWorkflowRunsRequestSchema,
  ListWorkflowRunsResponseSchema,
} from "@/gen/drift/v1/run_pb"
import {
  CreateSettingRequestSchema,
  CreateSettingResponseSchema,
  ListSettingHistoryRequestSchema,
  ListSettingHistoryResponseSchema,
  ListSettingsRequestSchema,
  ListSettingsResponseSchema,
  TransitionSettingRequestSchema,
  TransitionSettingResponseSchema,
  UpdateSettingRequestSchema,
  UpdateSettingResponseSchema,
  type Setting,
  type SettingScope,
} from "@/gen/drift/v1/settings_pb"
import { ListSkillsRequestSchema, ListSkillsResponseSchema } from "@/gen/drift/v1/skill_pb"
import { ListWorkflowsRequestSchema, ListWorkflowsResponseSchema } from "@/gen/drift/v1/workflow_pb"
import { ConnectJsonClient, requestContext, workspaceRef } from "@/lib/api/connect-json"

const listPage = create(PageRequestSchema, { pageSize: 200 })

function resourceRef(workspaceId: string, resourceId: string) {
  return create(ResourceRefSchema, { workspace: workspaceRef(workspaceId), resourceId })
}

class TypedConnectClient {
  constructor(
    private readonly json: ConnectJsonClient,
    private readonly serviceName: string,
  ) {}

  call<Request extends DescMessage, Response extends DescMessage>(
    method: string,
    requestSchema: Request,
    responseSchema: Response,
    init: MessageInitShape<Request>,
  ): Promise<MessageShape<Response>> {
    return this.json.call(this.serviceName, method, requestSchema, responseSchema, init)
  }
}

export class DeviceClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.DeviceService")
  }
  listDevices(workspaceId: string) {
    return this.rpc.call("ListDevices", ListDevicesRequestSchema, ListDevicesResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
  getDevice(workspaceId: string, deviceId: string) {
    return this.rpc.call("GetDevice", GetDeviceRequestSchema, GetDeviceResponseSchema, { workspace: workspaceRef(workspaceId), deviceId })
  }
}

export class NetworkProfileClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.NetworkProfileService")
  }
  listNetworkProfiles(workspaceId: string) {
    return this.rpc.call("ListNetworkProfiles", ListNetworkProfilesRequestSchema, ListNetworkProfilesResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
  createNetworkProfile(requestId: string, profile: NetworkProfile) {
    return this.rpc.call("CreateNetworkProfile", CreateNetworkProfileRequestSchema, CreateNetworkProfileResponseSchema, {
      context: requestContext({ requestId }),
      profile,
    })
  }
  updateNetworkProfile(requestId: string, profile: NetworkProfile, expectedRowVersion: bigint) {
    return this.rpc.call("UpdateNetworkProfile", UpdateNetworkProfileRequestSchema, UpdateNetworkProfileResponseSchema, {
      context: requestContext({ requestId }),
      profile,
      expectedRowVersion,
    })
  }
}

export class DiscoveryClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.DiscoveryService")
  }
  startScan(requestId: string, workspaceId: string, networkProfileId: string) {
    return this.rpc.call("StartScan", StartScanRequestSchema, StartScanResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      networkProfileId,
    })
  }
  decideScanCandidate(requestId: string, workspaceId: string, candidateId: string, approve: boolean, reason: string) {
    return this.rpc.call("DecideScanCandidate", DecideScanCandidateRequestSchema, DecideScanCandidateResponseSchema, {
      context: requestContext({ requestId }),
      candidate: resourceRef(workspaceId, candidateId),
      approve,
      reason,
    })
  }
  registerScanCandidate(requestId: string, workspaceId: string, candidateId: string, deviceDisplayName: string) {
    return this.rpc.call("RegisterScanCandidate", RegisterScanCandidateRequestSchema, RegisterScanCandidateResponseSchema, {
      context: requestContext({ requestId }),
      candidate: resourceRef(workspaceId, candidateId),
      deviceDisplayName,
    })
  }
  listScanRuns(workspaceId: string) {
    return this.rpc.call("ListScanRuns", ListScanRunsRequestSchema, ListScanRunsResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
  listScanCandidates(workspaceId: string) {
    return this.rpc.call("ListScanCandidates", ListScanCandidatesRequestSchema, ListScanCandidatesResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
}

export class GroupClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.GroupService")
  }
  listDeviceGroups(workspaceId: string) {
    return this.rpc.call("ListDeviceGroups", ListDeviceGroupsRequestSchema, ListDeviceGroupsResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
  moveDeviceToGroup(requestId: string, workspaceId: string, deviceId: string, groupId: string, position: number) {
    return this.rpc.call("MoveDeviceToGroup", MoveDeviceToGroupRequestSchema, MoveDeviceToGroupResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      deviceId,
      groupId,
      position,
    })
  }
}

export class EndpointClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.EndpointService")
  }
  listDeviceEndpoints(workspaceId: string, deviceId = "") {
    return this.rpc.call("ListDeviceEndpoints", ListDeviceEndpointsRequestSchema, ListDeviceEndpointsResponseSchema, {
      workspace: workspaceRef(workspaceId),
      deviceId,
      page: listPage,
    })
  }
}

export class ObservationClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.ObservationService")
  }
  getObservationSnapshot(workspaceId: string, observationId: string) {
    return this.rpc.call("GetObservationSnapshot", GetObservationSnapshotRequestSchema, GetObservationSnapshotResponseSchema, {
      observation: resourceRef(workspaceId, observationId),
    })
  }
  listObservationSnapshots(workspaceId: string, deviceId = "") {
    return this.rpc.call("ListObservationSnapshots", ListObservationSnapshotsRequestSchema, ListObservationSnapshotsResponseSchema, {
      workspace: workspaceRef(workspaceId),
      deviceId,
      page: listPage,
    })
  }
}

export class EventClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.EventService")
  }
  listOperationalEvents(workspaceId: string) {
    return this.rpc.call("ListOperationalEvents", ListOperationalEventsRequestSchema, ListOperationalEventsResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
  listAuditEvents(workspaceId: string) {
    return this.rpc.call("ListAuditEvents", ListAuditEventsRequestSchema, ListAuditEventsResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
}

export class EdgeAgentClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.EdgeAgentService")
  }
  listEdgeAgents(workspaceId: string) {
    return this.rpc.call("ListEdgeAgents", ListEdgeAgentsRequestSchema, ListEdgeAgentsResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
  getEdgeAgent(workspaceId: string, edgeAgentId: string) {
    return this.rpc.call("GetEdgeAgent", GetEdgeAgentRequestSchema, GetEdgeAgentResponseSchema, { edgeAgent: resourceRef(workspaceId, edgeAgentId) })
  }
}

export class LeaseClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.LeaseService")
  }
  acquireDeviceLease(requestId: string, workspaceId: string, deviceId: string, controlSessionId: string) {
    return this.rpc.call("AcquireDeviceLease", AcquireDeviceLeaseRequestSchema, AcquireDeviceLeaseResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      deviceId,
      controlSessionId,
    })
  }
  renewDeviceLease(requestId: string, workspaceId: string, leaseId: string, fencingToken: bigint) {
    return this.rpc.call("RenewDeviceLease", RenewDeviceLeaseRequestSchema, RenewDeviceLeaseResponseSchema, {
      context: requestContext({ requestId }),
      lease: resourceRef(workspaceId, leaseId),
      fencingToken,
    })
  }
  releaseDeviceLease(requestId: string, workspaceId: string, leaseId: string, fencingToken: bigint) {
    return this.rpc.call("ReleaseDeviceLease", ReleaseDeviceLeaseRequestSchema, ReleaseDeviceLeaseResponseSchema, {
      context: requestContext({ requestId }),
      lease: resourceRef(workspaceId, leaseId),
      fencingToken,
    })
  }
  listDeviceLeases(workspaceId: string) {
    return this.rpc.call("ListDeviceLeases", ListDeviceLeasesRequestSchema, ListDeviceLeasesResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
  openControlSession(requestId: string, workspaceId: string) {
    return this.rpc.call("OpenControlSession", OpenControlSessionRequestSchema, OpenControlSessionResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
    })
  }
  closeControlSession(requestId: string, workspaceId: string, sessionId: string) {
    return this.rpc.call("CloseControlSession", CloseControlSessionRequestSchema, CloseControlSessionResponseSchema, {
      context: requestContext({ requestId }),
      session: resourceRef(workspaceId, sessionId),
    })
  }
  listControlSessions(workspaceId: string) {
    return this.rpc.call("ListControlSessions", ListControlSessionsRequestSchema, ListControlSessionsResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
}

export class ActionClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.ActionService")
  }
  submitAction(requestId: string, init: MessageInitShape<typeof SubmitActionRequestSchema>) {
    return this.rpc.call("SubmitAction", SubmitActionRequestSchema, SubmitActionResponseSchema, {
      ...init,
      context: requestContext({ requestId }),
    })
  }
}

export class AccountClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.AccountService")
  }
  listAccountReferences(workspaceId: string) {
    return this.rpc.call("ListAccountReferences", ListAccountReferencesRequestSchema, ListAccountReferencesResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
  listAccountSources(workspaceId: string) {
    return this.rpc.call("ListAccountSources", ListAccountSourcesRequestSchema, ListAccountSourcesResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
  listAccountServiceStates(workspaceId: string, accountId = "") {
    return this.rpc.call("ListAccountServiceStates", ListAccountServiceStatesRequestSchema, ListAccountServiceStatesResponseSchema, { workspace: workspaceRef(workspaceId), accountId, page: listPage })
  }
  listAccountServiceStateHistory(workspaceId: string, accountId = "") {
    return this.rpc.call("ListAccountServiceStateHistory", ListAccountServiceStateHistoryRequestSchema, ListAccountServiceStateHistoryResponseSchema, { workspace: workspaceRef(workspaceId), accountId, page: listPage })
  }
  listAccountRuns(workspaceId: string, accountId = "") {
    return this.rpc.call("ListAccountRuns", ListAccountRunsRequestSchema, ListAccountRunsResponseSchema, { workspace: workspaceRef(workspaceId), accountId, page: listPage })
  }
  listAccountRunEvents(workspaceId: string, runId = "") {
    return this.rpc.call("ListAccountRunEvents", ListAccountRunEventsRequestSchema, ListAccountRunEventsResponseSchema, { workspace: workspaceRef(workspaceId), runId, page: listPage })
  }
  listAccountDeviceAssignments(workspaceId: string) {
    return this.rpc.call("ListAccountDeviceAssignments", ListAccountDeviceAssignmentsRequestSchema, ListAccountDeviceAssignmentsResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
  listAccountSyncEvents(workspaceId: string) {
    return this.rpc.call("ListAccountSyncEvents", ListAccountSyncEventsRequestSchema, ListAccountSyncEventsResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
  createAccountSource(requestId: string, workspaceId: string, input: { provider: string; displayName: string; externalReference: string; metadataJson: string }) {
    return this.rpc.call("CreateAccountSource", CreateAccountSourceRequestSchema, CreateAccountSourceResponseSchema, {
      context: requestContext({ requestId }),
      source: { workspace: workspaceRef(workspaceId), ...input },
    })
  }
  updateAccountSource(requestId: string, workspaceId: string, sourceId: string, input: { displayName: string; externalReference: string; metadataJson: string; rowVersion: number }) {
    return this.rpc.call("UpdateAccountSource", UpdateAccountSourceRequestSchema, UpdateAccountSourceResponseSchema, {
      context: requestContext({ requestId }),
      source: resourceRef(workspaceId, sourceId),
      displayName: input.displayName,
      externalReference: input.externalReference,
      metadataJson: input.metadataJson,
      expectedRowVersion: BigInt(input.rowVersion),
    })
  }
  transitionAccountSource(requestId: string, workspaceId: string, sourceId: string, state: AccountSourceState, rowVersion: number) {
    return this.rpc.call("TransitionAccountSource", TransitionAccountSourceRequestSchema, TransitionAccountSourceResponseSchema, {
      context: requestContext({ requestId }),
      source: resourceRef(workspaceId, sourceId),
      state,
      expectedRowVersion: BigInt(rowVersion),
    })
  }
  createAccount(requestId: string, workspaceId: string, input: { sourceId: string; externalReference: string; label: string; metadataJson: string }) {
    return this.rpc.call("CreateAccount", CreateAccountRequestSchema, CreateAccountResponseSchema, {
      context: requestContext({ requestId }),
      account: {
        workspace: workspaceRef(workspaceId),
        sourceId: input.sourceId,
        externalReference: input.externalReference,
        displayName: input.label,
        metadataJson: input.metadataJson,
      },
    })
  }
  updateAccount(requestId: string, workspaceId: string, accountId: string, input: { externalReference: string; label: string; metadataJson: string; rowVersion: number }) {
    return this.rpc.call("UpdateAccount", UpdateAccountRequestSchema, UpdateAccountResponseSchema, {
      context: requestContext({ requestId }),
      account: resourceRef(workspaceId, accountId),
      externalReference: input.externalReference,
      displayName: input.label,
      metadataJson: input.metadataJson,
      expectedRowVersion: BigInt(input.rowVersion),
    })
  }
  updateAccountState(requestId: string, workspaceId: string, accountId: string, state: AccountState, rowVersion: number) {
    return this.rpc.call("UpdateAccountState", UpdateAccountStateRequestSchema, UpdateAccountStateResponseSchema, {
      context: requestContext({ requestId }),
      account: resourceRef(workspaceId, accountId),
      state,
      expectedRowVersion: BigInt(rowVersion),
    })
  }
  assignAccountDevice(requestId: string, workspaceId: string, accountId: string, deviceId: string) {
    return this.rpc.call("AssignAccountDevice", AssignAccountDeviceRequestSchema, AssignAccountDeviceResponseSchema, {
      context: requestContext({ requestId }),
      account: resourceRef(workspaceId, accountId),
      device: resourceRef(workspaceId, deviceId),
    })
  }
  endAccountDeviceAssignment(requestId: string, workspaceId: string, assignmentId: string) {
    return this.rpc.call("EndAccountDeviceAssignment", EndAccountDeviceAssignmentRequestSchema, EndAccountDeviceAssignmentResponseSchema, {
      context: requestContext({ requestId }),
      assignment: resourceRef(workspaceId, assignmentId),
    })
  }
}

export class SettingsClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.SettingsService")
  }
  listSettings(workspaceId: string, scope: SettingScope) {
    return this.rpc.call("ListSettings", ListSettingsRequestSchema, ListSettingsResponseSchema, { workspace: workspaceRef(workspaceId), scope, page: listPage })
  }
  listSettingHistory(workspaceId: string, settingId = "") {
    return this.rpc.call("ListSettingHistory", ListSettingHistoryRequestSchema, ListSettingHistoryResponseSchema, { workspace: workspaceRef(workspaceId), settingId, page: listPage })
  }
  createSetting(requestId: string, setting: Setting) {
    return this.rpc.call("CreateSetting", CreateSettingRequestSchema, CreateSettingResponseSchema, { context: requestContext({ requestId }), setting })
  }
  updateSetting(requestId: string, workspaceId: string, settingId: string, valueJson: string, rowVersion: number) {
    return this.rpc.call("UpdateSetting", UpdateSettingRequestSchema, UpdateSettingResponseSchema, {
      context: requestContext({ requestId }),
      setting: resourceRef(workspaceId, settingId),
      valueJson,
      expectedRowVersion: BigInt(rowVersion),
    })
  }
  transitionSetting(requestId: string, workspaceId: string, settingId: string, state: string, rowVersion: number) {
    return this.rpc.call("TransitionSetting", TransitionSettingRequestSchema, TransitionSettingResponseSchema, {
      context: requestContext({ requestId }),
      setting: resourceRef(workspaceId, settingId),
      state,
      expectedRowVersion: BigInt(rowVersion),
    })
  }
}

export class PolicyClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.PolicyService")
  }
  listPolicies(workspaceId: string) {
    return this.rpc.call("ListPolicies", ListPoliciesRequestSchema, ListPoliciesResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
  listPolicyDecisions(workspaceId: string) {
    return this.rpc.call("ListPolicyDecisions", ListPolicyDecisionsRequestSchema, ListPolicyDecisionsResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
  createPolicyVersion(requestId: string, workspaceId: string, basePolicyId: string, ruleJson: string) {
    return this.rpc.call("CreatePolicyVersion", CreatePolicyVersionRequestSchema, CreatePolicyVersionResponseSchema, {
      context: requestContext({ requestId }),
      basePolicy: resourceRef(workspaceId, basePolicyId),
      ruleJson,
    })
  }
  activatePolicy(requestId: string, workspaceId: string, policyId: string, rowVersion: number) {
    return this.rpc.call("ActivatePolicy", ActivatePolicyRequestSchema, ActivatePolicyResponseSchema, {
      context: requestContext({ requestId }),
      policy: resourceRef(workspaceId, policyId),
      expectedRowVersion: BigInt(rowVersion),
    })
  }
  retirePolicy(requestId: string, workspaceId: string, policyId: string, rowVersion: number) {
    return this.rpc.call("RetirePolicy", RetirePolicyRequestSchema, RetirePolicyResponseSchema, {
      context: requestContext({ requestId }),
      policy: resourceRef(workspaceId, policyId),
      expectedRowVersion: BigInt(rowVersion),
    })
  }
}

export class WorkflowClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.WorkflowService")
  }
  listWorkflows(workspaceId: string) {
    return this.rpc.call("ListWorkflows", ListWorkflowsRequestSchema, ListWorkflowsResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
}

export class RunClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.RunService")
  }
  listWorkflowRuns(workspaceId: string) {
    return this.rpc.call("ListWorkflowRuns", ListWorkflowRunsRequestSchema, ListWorkflowRunsResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
  cancelWorkflowRun(requestId: string, workspaceId: string, runId: string) {
    return this.rpc.call("CancelWorkflowRun", CancelWorkflowRunRequestSchema, CancelWorkflowRunResponseSchema, {
      context: requestContext({ requestId }),
      run: resourceRef(workspaceId, runId),
    })
  }
  listRunTargets(workspaceId: string, runId = "") {
    return this.rpc.call("ListRunTargets", ListRunTargetsRequestSchema, ListRunTargetsResponseSchema, {
      workspace: workspaceRef(workspaceId),
      runId,
      page: listPage,
    })
  }
}

export class ArtifactClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.ArtifactService")
  }
  listArtifacts(workspaceId: string, actorId: string) {
    return this.rpc.call("ListArtifacts", ListArtifactsRequestSchema, ListArtifactsResponseSchema, { workspace: workspaceRef(workspaceId), actorId, limit: 200 })
  }
  readArtifact(workspaceId: string, artifactId: string, actorId: string) {
    return this.rpc.call("ReadArtifact", ReadArtifactRequestSchema, ReadArtifactResponseSchema, { workspace: workspaceRef(workspaceId), artifactId, actorId })
  }
  deleteArtifact(workspaceId: string, artifactId: string, actorId: string) {
    return this.rpc.call("DeleteArtifact", DeleteArtifactRequestSchema, DeleteArtifactResponseSchema, { workspace: workspaceRef(workspaceId), artifactId, actorId })
  }
  getStorageHealth(workspaceId: string, actorId: string) {
    return this.rpc.call("GetStorageHealth", GetStorageHealthRequestSchema, GetStorageHealthResponseSchema, { workspace: workspaceRef(workspaceId), actorId })
  }
}

export class AutomationAgentClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.AutomationAgentService")
  }
  listAutomationAgents(workspaceId: string) {
    return this.rpc.call("ListAutomationAgents", ListAutomationAgentsRequestSchema, ListAutomationAgentsResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
}

export class RecordingClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.RecordingService")
  }
  listRecordingSessions(workspaceId: string) {
    return this.rpc.call("ListRecordingSessions", ListRecordingSessionsRequestSchema, ListRecordingSessionsResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
}

export class SkillClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.SkillService")
  }
  listSkills(workspaceId: string) {
    return this.rpc.call("ListSkills", ListSkillsRequestSchema, ListSkillsResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
}

export class WorkspaceClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.WorkspaceService")
  }
  listWorkspaces() {
    return this.rpc.call("ListWorkspaces", ListWorkspacesRequestSchema, ListWorkspacesResponseSchema, { page: listPage })
  }
  getWorkspace(workspaceId: string) {
    return this.rpc.call("GetWorkspace", GetWorkspaceRequestSchema, GetWorkspaceResponseSchema, { workspace: workspaceRef(workspaceId) })
  }
}

export interface ControlPlaneServices {
  device: DeviceClient
  networkProfile: NetworkProfileClient
  discovery: DiscoveryClient
  group: GroupClient
  endpoint: EndpointClient
  observation: ObservationClient
  event: EventClient
  edgeAgent: EdgeAgentClient
  lease: LeaseClient
  action: ActionClient
  account: AccountClient
  settings: SettingsClient
  policy: PolicyClient
  workflow: WorkflowClient
  run: RunClient
  artifact: ArtifactClient
  automationAgent: AutomationAgentClient
  recording: RecordingClient
  skill: SkillClient
  workspace: WorkspaceClient
}

export function createControlPlaneServices(json: ConnectJsonClient): ControlPlaneServices {
  return {
    device: new DeviceClient(json),
    networkProfile: new NetworkProfileClient(json),
    discovery: new DiscoveryClient(json),
    group: new GroupClient(json),
    endpoint: new EndpointClient(json),
    observation: new ObservationClient(json),
    event: new EventClient(json),
    edgeAgent: new EdgeAgentClient(json),
    lease: new LeaseClient(json),
    action: new ActionClient(json),
    account: new AccountClient(json),
    settings: new SettingsClient(json),
    policy: new PolicyClient(json),
    workflow: new WorkflowClient(json),
    run: new RunClient(json),
    artifact: new ArtifactClient(json),
    automationAgent: new AutomationAgentClient(json),
    recording: new RecordingClient(json),
    skill: new SkillClient(json),
    workspace: new WorkspaceClient(json),
  }
}
