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
import { GetHaltRequestSchema, GetHaltResponseSchema, HaltState, SetHaltRequestSchema, SetHaltResponseSchema, SubmitActionRequestSchema, SubmitActionResponseSchema } from "@/gen/drift/v1/action_pb"
import { KeyEventRequestSchema, KeyEventResponseSchema, SwipeRequestSchema, SwipeResponseSchema, TapRequestSchema, TapResponseSchema, TypeTextRequestSchema, TypeTextResponseSchema } from "@/gen/drift/v1/device_input_pb"
import {
  GetMirrorCapacityRequestSchema,
  GetMirrorCapacityResponseSchema,
  GetMirrorStreamRequestSchema,
  GetMirrorStreamResponseSchema,
  NegotiateMirrorStreamRequestSchema,
  NegotiateMirrorStreamResponseSchema,
  StartMirrorStreamRequestSchema,
  StartMirrorStreamResponseSchema,
  StopMirrorStreamRequestSchema,
  StopMirrorStreamResponseSchema,
  type MirrorStream,
} from "@/gen/drift/v1/device_mirror_pb"
import { liveMirrorCopy, liveStreamView, mirrorCapacityView, previewRequestFor, purposeRequestFor, transportRequestFor, type LiveMirrorPreview, type LiveMirrorTransportChoice, type LiveMirrorViewerPurpose, type LiveStreamView, type MirrorCapacityView } from "@/lib/live-mirror"
import { ApplyDeviceSettingRequestSchema, ApplyDeviceSettingResponseSchema, ApplyDeviceSettingsRequestSchema, ApplyDeviceSettingsResponseSchema, DeviceSetting } from "@/gen/drift/v1/device_settings_pb"
import { DeviceOperation, RunAdvancedCommandRequestSchema, RunAdvancedCommandResponseSchema, RunDeviceOperationRequestSchema, RunDeviceOperationResponseSchema } from "@/gen/drift/v1/device_operations_pb"
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
  AssignAutomationAgentDeviceRequestSchema,
  AssignAutomationAgentDeviceResponseSchema,
  CreateAutomationAgentRequestSchema,
  CreateAutomationAgentResponseSchema,
  ListAutomationAgentsRequestSchema,
  ListAutomationAgentsResponseSchema,
} from "@/gen/drift/v1/automation_agent_pb"
import { PageRequestSchema, ResourceRefSchema } from "@/gen/drift/v1/common_pb"
import {
  StopGridPreviewsRequestSchema,
  StopGridPreviewsResponseSchema,
  SyncGridPreviewsRequestSchema,
  SyncGridPreviewsResponseSchema,
  type SyncGridPreviewsResponse,
} from "@/gen/drift/v1/grid_preview_pb"
import {
  GetDeviceRequestSchema,
  GetDeviceResponseSchema,
  ListDevicesRequestSchema,
  ListDevicesResponseSchema,
  RefreshDeviceDiagnosticsRequestSchema,
  RefreshDeviceDiagnosticsResponseSchema,
} from "@/gen/drift/v1/device_pb"
import {
  ListScanRunsRequestSchema,
  ListScanRunsResponseSchema,
  StartScanRequestSchema,
  StartScanResponseSchema,
  StartRangeScanRequestSchema,
  StartRangeScanResponseSchema,
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
  type DeviceEndpoint,
} from "@/gen/drift/v1/endpoint_pb"
import {
  ListAuditEventsRequestSchema,
  ListAuditEventsResponseSchema,
  ListOperationalEventsRequestSchema,
  ListOperationalEventsResponseSchema,
} from "@/gen/drift/v1/event_pb"
import {
  CreateDeviceGroupRequestSchema,
  CreateDeviceGroupResponseSchema,
  DeleteDeviceGroupRequestSchema,
  DeleteDeviceGroupResponseSchema,
  ListDeviceGroupsRequestSchema,
  ListDeviceGroupsResponseSchema,
  MoveDeviceToGroupRequestSchema,
  MoveDeviceToGroupResponseSchema,
  RemoveDeviceFromGroupRequestSchema,
  RemoveDeviceFromGroupResponseSchema,
  RenameDeviceGroupRequestSchema,
  RenameDeviceGroupResponseSchema,
  ReorderDeviceGroupsRequestSchema,
  ReorderDeviceGroupsResponseSchema,
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
  DeleteNetworkProfileRequestSchema,
  DeleteNetworkProfileResponseSchema,
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
  CreateRecordingSessionRequestSchema,
  CreateRecordingSessionResponseSchema,
  DeleteRecordingSessionRequestSchema,
  DeleteRecordingSessionResponseSchema,
  DiscardRecordingSessionRequestSchema,
  DiscardRecordingSessionResponseSchema,
  ListRecordingSessionsRequestSchema,
  ListRecordingSessionsResponseSchema,
  StartRecordingSessionRequestSchema,
  StartRecordingSessionResponseSchema,
  StopRecordingSessionRequestSchema,
  StopRecordingSessionResponseSchema,
} from "@/gen/drift/v1/recording_pb"
import {
  CancelWorkflowRunRequestSchema,
  CancelWorkflowRunResponseSchema,
  ListRunTargetsRequestSchema,
  ListRunTargetsResponseSchema,
  ListWorkflowRunsRequestSchema,
  ListWorkflowRunsResponseSchema,
  StartWorkflowRunRequestSchema,
  StartWorkflowRunResponseSchema,
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
import {
  ListSkillsRequestSchema,
  ListSkillsResponseSchema,
  ListSkillVersionsRequestSchema,
  ListSkillVersionsResponseSchema,
  PublishSkillVersionRequestSchema,
  PublishSkillVersionResponseSchema,
  ReviewSkillVersionRequestSchema,
  ReviewSkillVersionResponseSchema,
} from "@/gen/drift/v1/skill_pb"
import {
  CreateWorkflowRequestSchema,
  CreateWorkflowResponseSchema,
  ListWorkflowsRequestSchema,
  ListWorkflowsResponseSchema,
  PublishWorkflowVersionRequestSchema,
  PublishWorkflowVersionResponseSchema,
} from "@/gen/drift/v1/workflow_pb"
import {
  ListMirrorSessionsRequestSchema,
  ListMirrorSessionsResponseSchema,
  StartMirrorPreviewRequestSchema,
  StartMirrorPreviewResponseSchema,
  StopMirrorPreviewRequestSchema,
  StopMirrorPreviewResponseSchema,
} from "@/gen/drift/v1/mirror_pb"
import {
  BeginRuntimeReconnectRequestSchema,
  BeginRuntimeReconnectResponseSchema,
  CompleteRuntimeReconnectRequestSchema,
  CompleteRuntimeReconnectResponseSchema,
  ConfirmIndeterminateActionRequestSchema,
  ConfirmIndeterminateActionResponseSchema,
  ConfirmSpoolReplayRequestSchema,
  ConfirmSpoolReplayResponseSchema,
  DisconnectRuntimeRequestSchema,
  DisconnectRuntimeResponseSchema,
  GetRuntimeStatusRequestSchema,
  GetRuntimeStatusResponseSchema,
} from "@/gen/drift/v1/runtime_pb"
import { ConnectJsonClient, ConnectJsonError, connectCodeForHttpStatus, defaultOperatorId, newRequestId, requestContext, textReferencePath, textReferenceWorkspaceHeader, workspaceRef } from "@/lib/api/connect-json"
import {
  ActivateFleetRequestSchema,
  ActivateFleetResponseSchema,
  ConnectEndpointRequestSchema,
  ConnectEndpointResponseSchema,
  RestartServerRequestSchema,
  RestartServerResponseSchema,
} from "@/gen/drift/v1/connection_pb"

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
  refreshDeviceDiagnostics(workspaceId: string, deviceId = "") {
    return this.rpc.call("RefreshDeviceDiagnostics", RefreshDeviceDiagnosticsRequestSchema, RefreshDeviceDiagnosticsResponseSchema, { workspace: workspaceRef(workspaceId), deviceId })
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
  updateNetworkProfile(requestId: string, profile: NetworkProfile) {
    return this.rpc.call("UpdateNetworkProfile", UpdateNetworkProfileRequestSchema, UpdateNetworkProfileResponseSchema, {
      context: requestContext({ requestId }),
      profile,

    })
  }
  deleteNetworkProfile(requestId: string, workspaceId: string, profileId: string) {
    return this.rpc.call("DeleteNetworkProfile", DeleteNetworkProfileRequestSchema, DeleteNetworkProfileResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      networkProfileId: profileId,
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
  startRangeScan(requestId: string, workspaceId: string, addressPolicy: string, port: number) {
    return this.rpc.call("StartRangeScan", StartRangeScanRequestSchema, StartRangeScanResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      addressPolicy,
      port,
    })
  }
  listScanRuns(workspaceId: string) {
    return this.rpc.call("ListScanRuns", ListScanRunsRequestSchema, ListScanRunsResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
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
  createDeviceGroup(requestId: string, workspaceId: string, displayName: string) {
    return this.rpc.call("CreateDeviceGroup", CreateDeviceGroupRequestSchema, CreateDeviceGroupResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      displayName,
    })
  }
  renameDeviceGroup(requestId: string, workspaceId: string, groupId: string, displayName: string, rowVersion: number) {
    return this.rpc.call("RenameDeviceGroup", RenameDeviceGroupRequestSchema, RenameDeviceGroupResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      groupId,
      displayName,
      rowVersion: BigInt(rowVersion),
    })
  }
  deleteDeviceGroup(requestId: string, workspaceId: string, groupId: string, rowVersion: number, confirmed: boolean) {
    return this.rpc.call("DeleteDeviceGroup", DeleteDeviceGroupRequestSchema, DeleteDeviceGroupResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      groupId,
      rowVersion: BigInt(rowVersion),
      confirmed,
    })
  }
  reorderDeviceGroups(requestId: string, workspaceId: string, groupIds: readonly string[]) {
    return this.rpc.call("ReorderDeviceGroups", ReorderDeviceGroupsRequestSchema, ReorderDeviceGroupsResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      groupIds: [...groupIds],
    })
  }
  removeDeviceFromGroup(requestId: string, workspaceId: string, deviceId: string) {
    return this.rpc.call("RemoveDeviceFromGroup", RemoveDeviceFromGroupRequestSchema, RemoveDeviceFromGroupResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      deviceId,
    })
  }
}

export class EndpointClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.EndpointService")
  }
  async listDeviceEndpoints(workspaceId: string, deviceId = "") {
    // Endpoint history is append-only and can exceed the normal 200-row page
    // even for a modest fleet. Reading only the first page makes current rows
    // disappear behind old history, which leaves an online device with blank
    // serial/host/port cells. Follow the server cursor until the whole bounded
    // workspace projection has been joined.
    const endpoints: DeviceEndpoint[] = []
    let pageToken = ""
    do {
      const response = await this.rpc.call("ListDeviceEndpoints", ListDeviceEndpointsRequestSchema, ListDeviceEndpointsResponseSchema, {
        workspace: workspaceRef(workspaceId),
        deviceId,
        page: create(PageRequestSchema, { pageSize: 200, pageToken }),
      })
      endpoints.push(...response.endpoints)
      pageToken = response.page?.nextPageToken ?? ""
    } while (pageToken)
    return { endpoints }
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
  getHalt(workspaceId: string) { return this.rpc.call("GetHalt", GetHaltRequestSchema, GetHaltResponseSchema, { workspace: workspaceRef(workspaceId) }) }
  setHalt(requestId: string, workspaceId: string, state: HaltState, reason: string) { return this.rpc.call("SetHalt", SetHaltRequestSchema, SetHaltResponseSchema, { context: requestContext({ requestId }), workspace: workspaceRef(workspaceId), state, reason }) }
}

export class DeviceInputClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) { this.rpc = new TypedConnectClient(json, "drift.v1.DeviceInputService") }
  tap(requestId: string, init: MessageInitShape<typeof TapRequestSchema>) { return this.rpc.call("Tap", TapRequestSchema, TapResponseSchema, { ...init, context: requestContext({ requestId }) }) }
  swipe(requestId: string, init: MessageInitShape<typeof SwipeRequestSchema>) { return this.rpc.call("Swipe", SwipeRequestSchema, SwipeResponseSchema, { ...init, context: requestContext({ requestId }) }) }
  keyEvent(requestId: string, init: MessageInitShape<typeof KeyEventRequestSchema>) { return this.rpc.call("KeyEvent", KeyEventRequestSchema, KeyEventResponseSchema, { ...init, context: requestContext({ requestId }) }) }
  /**
   * typeText submits one typed-text entry by REFERENCE. The request names the
   * handle a registration returned and the length of the value it holds; the
   * value itself has no field here to travel in, and it never enters this call.
   */
  typeText(requestId: string, init: MessageInitShape<typeof TypeTextRequestSchema>) { return this.rpc.call("TypeText", TypeTextRequestSchema, TypeTextResponseSchema, { ...init, context: requestContext({ requestId }) }) }
}

/**
 * TextReferenceClient is the port an operator's typed content enters the control
 * plane through, and the only one that carries content at all.
 *
 * The value is the request BODY, never a field of a message: a generated message
 * renders every populated field in its string, text, JSON and debug forms, and
 * protobuf-go has no per-field redaction, so a field is a place content is
 * logged, rendered in an error and persisted the first time anyone formats it. A
 * value registered here is released at dispatch, at most once, and expires.
 *
 * It is deliberately not part of the intent any component re-reads: a handle is
 * worthless on its own, and the console keeps no copy of the value after the
 * registration returns.
 */
export class TextReferenceClient {
  constructor(private readonly json: ConnectJsonClient) {}

  /**
   * register holds one value under one opaque handle, in the workspace that owns
   * it. The handle must be an opaque reference (letters, digits, and `_.:-`
   * only) — it is the one string this surface offers, and it must not be able to
   * carry content.
   *
   * A refusal is a refusal, not a partial success: this throws, and a caller that
   * swallowed it would dispatch a reference to nothing.
   */
  async register(handle: string, workspaceId: string, value: string): Promise<void> {
    const { url, headers } = this.json.endpoint(`${textReferencePath}${encodeURIComponent(handle)}`)
    const response = await fetch(url, {
      method: "POST",
      headers: { ...headers, [textReferenceWorkspaceHeader]: workspaceId, "content-type": "text/plain; charset=utf-8" },
      body: value,
    })
    if (!response.ok) {
      throw new ConnectJsonError(connectCodeForHttpStatus(response.status), textReferenceRefusalSentence(response.status))
    }
  }
}

/**
 * textReferenceRefusalSentence is a fixed sentence per status: the response body
 * is written by the surface that refuses it, and the console has nothing to add
 * to it — least of all the value it just failed to register.
 */
function textReferenceRefusalSentence(status: number): string {
  if (status === 409) return "That text reference handle is already held by an unreleased value."
  if (status === 413) return "That text is larger than the control plane registers."
  if (status === 400) return "The control plane refused the text reference registration."
  if (status === 404) return "The control plane did not find the text reference it was asked for."
  if (status === 503) return "The control plane is not registering text references right now."
  return "The control plane did not register the text reference."
}


export class DeviceSettingsClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) { this.rpc = new TypedConnectClient(json, "drift.v1.DeviceSettingsService") }
  /**
   * applyDeviceSettings applies the named settings to every device in the
   * registry that has a current transport endpoint. The request names no device
   * list: the control plane reads the fleet, because an operator action cannot
   * assert which devices are attached.
   *
   * `approvalGranted` is the operator's explicit approval. The control plane
   * requires it for a high-risk setting — autofill off rewrites a secure setting
   * — and that refusal is reported per device and per setting.
   */
  applyDeviceSettings(requestId: string, workspaceId: string, settings: readonly DeviceSetting[], approvalGranted: boolean) {
    return this.rpc.call("ApplyDeviceSettings", ApplyDeviceSettingsRequestSchema, ApplyDeviceSettingsResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      settings: [...settings],
      approvalGranted,
    })
  }

  /**
   * applyDeviceSetting applies ONE setting to ONE device: the per-device form.
   *
   * The device travels as its registry identity and never as a transport serial,
   * so this call cannot assert where a device is — the control plane resolves the
   * serial from the registry it observed. A device the registry does not hold is
   * refused with its own reason rather than resolved to another device.
   */
  applyDeviceSetting(requestId: string, workspaceId: string, deviceId: string, setting: DeviceSetting, approvalGranted: boolean) {
    return this.rpc.call("ApplyDeviceSetting", ApplyDeviceSettingRequestSchema, ApplyDeviceSettingResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      deviceId,
      setting,
      approvalGranted,
    })
  }
}

export class DeviceOperationsClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) { this.rpc = new TypedConnectClient(json, "drift.v1.DeviceOperationsService") }
  /**
   * runDeviceOperation runs ONE catalogued operation on ONE device.
   *
   * The device travels as its registry identity and never as a transport serial,
   * so this call cannot assert where a device is — the control plane resolves the
   * serial from the registry it observed. `fileName` is a bounded file NAME
   * inside the one device directory this product owns and is never a path, and
   * `artifactId` names bytes this workspace already holds, so no host path
   * reaches the control plane from here either.
   */
  runDeviceOperation(requestId: string, workspaceId: string, deviceId: string, operation: DeviceOperation, parameters: { fileName: string; artifactId: string; mediaType: string; packageName: string }, approvalGranted: boolean) {
    return this.rpc.call("RunDeviceOperation", RunDeviceOperationRequestSchema, RunDeviceOperationResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      deviceId,
      operation,
      fileName: parameters.fileName,
      artifactId: parameters.artifactId,
      mediaType: parameters.mediaType,
      packageName: parameters.packageName,
      approvalGranted,
    })
  }

  /**
   * runAdvancedCommand runs the operator's own argument array on ONE device,
   * after the operator confirmed the EXACT array carried here.
   *
   * The array travels as discrete entries, never as one joined command string, so
   * each argument is visibly separate in the audit record and the control plane
   * can spawn it without a shell.
   */
  runAdvancedCommand(requestId: string, workspaceId: string, deviceId: string, argv: readonly string[], confirmed: boolean, approvalGranted: boolean) {
    return this.rpc.call("RunAdvancedCommand", RunAdvancedCommandRequestSchema, RunAdvancedCommandResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      deviceId,
      argv: [...argv],
      confirmed,
      approvalGranted,
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
  createWorkflow(requestId: string, workspaceId: string, displayName: string) {
    return this.rpc.call("CreateWorkflow", CreateWorkflowRequestSchema, CreateWorkflowResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      displayName,
    })
  }
  publishWorkflowVersion(requestId: string, workspaceId: string, versionId: string) {
    return this.rpc.call("PublishWorkflowVersion", PublishWorkflowVersionRequestSchema, PublishWorkflowVersionResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      versionId,
    })
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
  startWorkflowRun(requestId: string, workspaceId: string, workflowId: string, deviceIds: readonly string[]) {
    return this.rpc.call("StartWorkflowRun", StartWorkflowRunRequestSchema, StartWorkflowRunResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      workflowId,
      deviceIds: [...deviceIds],
      concurrencyLimit: 1,
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
  createAutomationAgent(requestId: string, workspaceId: string, displayName: string) {
    return this.rpc.call("CreateAutomationAgent", CreateAutomationAgentRequestSchema, CreateAutomationAgentResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      displayName,
    })
  }
  assignAutomationAgentDevice(requestId: string, workspaceId: string, automationAgentId: string, deviceId: string) {
    return this.rpc.call("AssignAutomationAgentDevice", AssignAutomationAgentDeviceRequestSchema, AssignAutomationAgentDeviceResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      automationAgentId,
      deviceId,
    })
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
  createRecordingSession(requestId: string, workspaceId: string, deviceId: string) {
    return this.rpc.call("CreateRecordingSession", CreateRecordingSessionRequestSchema, CreateRecordingSessionResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      deviceId,
      source: "device",
    })
  }
  startRecordingSession(requestId: string, workspaceId: string, sessionId: string) {
    return this.rpc.call("StartRecordingSession", StartRecordingSessionRequestSchema, StartRecordingSessionResponseSchema, {
      context: requestContext({ requestId }),
      session: resourceRef(workspaceId, sessionId),
    })
  }
  stopRecordingSession(requestId: string, workspaceId: string, sessionId: string) {
    return this.rpc.call("StopRecordingSession", StopRecordingSessionRequestSchema, StopRecordingSessionResponseSchema, {
      context: requestContext({ requestId }),
      session: resourceRef(workspaceId, sessionId),
    })
  }
  discardRecordingSession(requestId: string, workspaceId: string, sessionId: string) {
    return this.rpc.call("DiscardRecordingSession", DiscardRecordingSessionRequestSchema, DiscardRecordingSessionResponseSchema, {
      context: requestContext({ requestId }),
      session: resourceRef(workspaceId, sessionId),
    })
  }
  deleteRecordingSession(requestId: string, workspaceId: string, sessionId: string, confirmed: boolean) {
    return this.rpc.call("DeleteRecordingSession", DeleteRecordingSessionRequestSchema, DeleteRecordingSessionResponseSchema, {
      context: requestContext({ requestId }),
      session: resourceRef(workspaceId, sessionId),
      confirmed,
    })
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
  listSkillVersions(workspaceId: string, skillId: string) {
    return this.rpc.call("ListSkillVersions", ListSkillVersionsRequestSchema, ListSkillVersionsResponseSchema, {
      skill: resourceRef(workspaceId, skillId),
      page: listPage,
    })
  }
  reviewSkillVersion(requestId: string, workspaceId: string, versionId: string, reason: string) {
    return this.rpc.call("ReviewSkillVersion", ReviewSkillVersionRequestSchema, ReviewSkillVersionResponseSchema, {
      context: requestContext({ requestId }),
      version: resourceRef(workspaceId, versionId),
      reason,
    })
  }
  publishSkillVersion(requestId: string, workspaceId: string, versionId: string, reason: string) {
    return this.rpc.call("PublishSkillVersion", PublishSkillVersionRequestSchema, PublishSkillVersionResponseSchema, {
      context: requestContext({ requestId }),
      version: resourceRef(workspaceId, versionId),
      reason,
    })
  }
}

/**
 * LiveStreamAnswer is one negotiated stream: the control plane's answer to the
 * browser's offer, and the stream as it stands after the handshake.
 */
export interface LiveStreamAnswer {
  answerSdp: string
  stream: LiveStreamView
}

/**
 * LiveMirrorClient is the port the console's live surface drives: open one
 * device's stream, negotiate a transport with this browser, read where it stands,
 * and end it.
 *
 * It is deliberately not part of ControlPlaneIntent. A stream is not a snapshot
 * mutation: it is opened when the big frame opens, ended when it closes, and its
 * state changes on its own schedule, so it is read on its own cadence rather than
 * folded into a projection the console re-reads every few seconds.
 *
 * The port carries no device address, no serial and no stream path a browser
 * could reach directly: it names a device in a workspace and receives this
 * service's own stream identity, which is the whole of what the frames hang off.
 */
export interface LiveMirrorClient {
  /**
   * startStream opens one device's live stream over the transport the operator
   * chose. The transport is asked for by name rather than left to the control
   * plane's default, because this console renders both and the operator picked
   * one; the control plane carries what was asked for or refuses it.
   *
   * The PURPOSE is asked for the same way, and for a sharper reason: the plane
   * spends its device-session capacity per purpose and keeps a place of it for the
   * operator's own frame, so a grid tile that opened without saying it was one
   * would spend the place the big frame needs. This console knows what it is
   * opening - a tile or the frame the operator works from - and says so.
   *
   * The PREVIEW is the workspace's own encode setting, and it is stated for an
   * ambient tile because the operator chose it and it is a bound the plane applies
   * to the picture they are shown. An operator's own frame states none: the plane
   * carries that frame at its own profile, so a level chosen for the grid can
   * never make the frame the work happens in blurry.
   */
  startStream(request: { workspaceId: string; deviceId: string; transport?: LiveMirrorTransportChoice; purpose: LiveMirrorViewerPurpose; preview?: LiveMirrorPreview }): Promise<LiveStreamView>
  /**
   * getCapacity reads the bound the control plane is actually carrying, which is
   * what decides how many tiles this console may subscribe. It is a read and
   * nothing else: the plane's capacity belongs to the deployment, and a console
   * that could set it would be a console that could starve the operator's own
   * frame.
   */
  getCapacity(workspaceId: string): Promise<MirrorCapacityView>
  negotiate(streamId: string, offerSdp: string): Promise<LiveStreamAnswer>
  stopStream(streamId: string): Promise<LiveStreamView>
  getStream(streamId: string): Promise<LiveStreamView>
  /**
   * streamEndpoint resolves the per-device stream endpoint a TCP stream is fetched
   * from, with the credentials this console reaches the control plane with.
   */
  streamEndpoint(path: string): { url: string; headers: Record<string, string> }
}

export class DeviceMirrorClient implements LiveMirrorClient {
  private readonly rpc: TypedConnectClient
  private readonly json: ConnectJsonClient
  private readonly operatorId: string
  constructor(json: ConnectJsonClient, operatorId = defaultOperatorId) {
    this.rpc = new TypedConnectClient(json, "drift.v1.DeviceMirrorService")
    this.json = json
    this.operatorId = operatorId
  }
  async startStream(request: { workspaceId: string; deviceId: string; transport?: LiveMirrorTransportChoice; purpose: LiveMirrorViewerPurpose; preview?: LiveMirrorPreview }): Promise<LiveStreamView> {
    const requestId = newRequestId()
    // The workspace's preview setting travels with the viewer that stated it and
    // bounds the grid's tiles only. An operator's own frame states nothing, which
    // is the plane's own profile for it (see previewRequestFor).
    const preview = previewRequestFor(request.purpose, request.preview)
    const response = await this.rpc.call("StartMirrorStream", StartMirrorStreamRequestSchema, StartMirrorStreamResponseSchema, {
      context: requestContext({ requestId, actorId: this.operatorId }),
      workspace: workspaceRef(request.workspaceId),
      deviceId: request.deviceId,
      transport: transportRequestFor(request.transport ?? "webrtc"),
      purpose: purposeRequestFor(request.purpose),
      previewQuality: preview.previewQuality,
      frameRate: preview.frameRate,
    })
    return requireStream(response.stream)
  }
  async getCapacity(workspaceId: string): Promise<MirrorCapacityView> {
    const response = await this.rpc.call("GetMirrorCapacity", GetMirrorCapacityRequestSchema, GetMirrorCapacityResponseSchema, {
      workspace: workspaceRef(workspaceId),
    })
    // An absent capacity is a control plane that did not answer the question, and
    // it must not read as a plane whose bound is zero: the console would then carry
    // no tiles at all and blame its own allocation for a plane it could not read.
    if (!response.capacity) throw new ConnectJsonError("internal", liveMirrorCopy.failure.noCapacity)
    return mirrorCapacityView(response.capacity)
  }
  streamEndpoint(path: string): { url: string; headers: Record<string, string> } {
    return this.json.endpoint(path)
  }
  async negotiate(streamId: string, offerSdp: string): Promise<LiveStreamAnswer> {
    const response = await this.rpc.call("NegotiateMirrorStream", NegotiateMirrorStreamRequestSchema, NegotiateMirrorStreamResponseSchema, {
      context: requestContext({ requestId: newRequestId(), actorId: this.operatorId }),
      streamId,
      offerSdp,
    })
    return { answerSdp: response.answerSdp, stream: requireStream(response.stream) }
  }
  async stopStream(streamId: string): Promise<LiveStreamView> {
    const response = await this.rpc.call("StopMirrorStream", StopMirrorStreamRequestSchema, StopMirrorStreamResponseSchema, {
      context: requestContext({ requestId: newRequestId(), actorId: this.operatorId }),
      streamId,
    })
    return requireStream(response.stream)
  }
  async getStream(streamId: string): Promise<LiveStreamView> {
    const response = await this.rpc.call("GetMirrorStream", GetMirrorStreamRequestSchema, GetMirrorStreamResponseSchema, { streamId })
    return requireStream(response.stream)
  }
}

/**
 * requireStream refuses a response that carried no stream, rather than reading it
 * as an empty one: an absent stream is a control plane that did not answer the
 * question, and it must not render as a stream that is merely not live yet.
 */
function requireStream(stream: MirrorStream | undefined): LiveStreamView {
  if (!stream) throw new ConnectJsonError("internal", liveMirrorCopy.failure.noStream)
  return liveStreamView(stream)
}

/**
 * GridPreviewClient is the port the console's fleet grid drives: reconcile the
 * plane's capture set with the devices this grid is drawing, and release it when
 * the grid goes away.
 *
 * It is deliberately not part of ControlPlaneIntent, for the same reason a stream
 * is not: a still is not a snapshot mutation. The plane OWNS the cadence - one
 * owned worker captures each subscribed device once per sweep - so this is a
 * reconciliation the console polls, not a capture it drives per device, and a
 * console that named the set it is drawing cannot fan the fleet's work out.
 *
 * The port carries no session, no viewer place and no stream identity: a still
 * spends no device session, so there is nothing here to allocate and no bound to
 * run out of. The one live session this console opens is the operator's own frame,
 * which is `LiveMirrorClient`'s business.
 */
export interface GridPreviewClient {
  /**
   * syncGridPreviews names EVERY device the grid is drawing, in the grid's own
   * order, and reads what the plane's cadence produced for each. The set is
   * reconciled rather than accumulated: the plane captures exactly the devices
   * named by the most recent request, so the console STATES the set every time
   * rather than adding to a subscription it would then have to remember to remove.
   */
  syncGridPreviews(request: { workspaceId: string; deviceIds: readonly string[] }): Promise<SyncGridPreviewsResponse>
  /**
   * stopGridPreviews releases the capture set: a device nothing is showing must not
   * stay captured. It is best-effort and idempotent - a second stop releases nobody
   * and says so - so a console that closed its grid twice is not an error.
   */
  stopGridPreviews(workspaceId: string): Promise<number>
}

export class DeviceGridPreviewClient implements GridPreviewClient {
  private readonly rpc: TypedConnectClient
  private readonly operatorId: string
  constructor(json: ConnectJsonClient, operatorId = defaultOperatorId) {
    this.rpc = new TypedConnectClient(json, "drift.v1.GridPreviewService")
    this.operatorId = operatorId
  }
  syncGridPreviews(request: { workspaceId: string; deviceIds: readonly string[] }): Promise<SyncGridPreviewsResponse> {
    return this.rpc.call("SyncGridPreviews", SyncGridPreviewsRequestSchema, SyncGridPreviewsResponseSchema, {
      context: requestContext({ requestId: newRequestId(), actorId: this.operatorId }),
      workspace: workspaceRef(request.workspaceId),
      // Every device the grid draws travels, one entry each, uncapped: this list IS
      // the plane's capture set, so a console that shortened it would silently stop
      // capturing every device past the cut.
      deviceIds: [...request.deviceIds],
    })
  }
  async stopGridPreviews(workspaceId: string): Promise<number> {
    const response = await this.rpc.call("StopGridPreviews", StopGridPreviewsRequestSchema, StopGridPreviewsResponseSchema, {
      context: requestContext({ requestId: newRequestId(), actorId: this.operatorId }),
      workspace: workspaceRef(workspaceId),
    })
    return response.released
  }
}

export class MirrorClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.MirrorService")
  }
  listMirrorSessions(workspaceId: string) {
    return this.rpc.call("ListMirrorSessions", ListMirrorSessionsRequestSchema, ListMirrorSessionsResponseSchema, { workspace: workspaceRef(workspaceId), page: listPage })
  }
  startMirrorPreview(requestId: string, workspaceId: string, sourceDeviceId: string, followerDeviceIds: readonly string[]) {
    return this.rpc.call("StartMirrorPreview", StartMirrorPreviewRequestSchema, StartMirrorPreviewResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      sourceDeviceId,
      followerDeviceIds: [...followerDeviceIds],
    })
  }
  stopMirrorPreview(requestId: string, workspaceId: string, sessionId: string) {
    return this.rpc.call("StopMirrorPreview", StopMirrorPreviewRequestSchema, StopMirrorPreviewResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      sessionId,
    })
  }
}

export class RuntimeClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.RuntimeService")
  }
  getRuntimeStatus(workspaceId: string) {
    return this.rpc.call("GetRuntimeStatus", GetRuntimeStatusRequestSchema, GetRuntimeStatusResponseSchema, { workspace: workspaceRef(workspaceId) })
  }
  disconnectRuntime(requestId: string, workspaceId: string, reason: string) {
    return this.rpc.call("DisconnectRuntime", DisconnectRuntimeRequestSchema, DisconnectRuntimeResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      reason,
    })
  }
  beginRuntimeReconnect(requestId: string, workspaceId: string) {
    return this.rpc.call("BeginRuntimeReconnect", BeginRuntimeReconnectRequestSchema, BeginRuntimeReconnectResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
    })
  }
  completeRuntimeReconnect(requestId: string, workspaceId: string, transportId: string, protocol: string) {
    return this.rpc.call("CompleteRuntimeReconnect", CompleteRuntimeReconnectRequestSchema, CompleteRuntimeReconnectResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      transportId,
      protocol,
    })
  }
  confirmSpoolReplay(requestId: string, workspaceId: string, sequence: number, confirm: boolean) {
    return this.rpc.call("ConfirmSpoolReplay", ConfirmSpoolReplayRequestSchema, ConfirmSpoolReplayResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      sequence: BigInt(sequence),
      confirm,
    })
  }
  confirmIndeterminateAction(requestId: string, workspaceId: string, actionId: string, confirm: boolean, resolution: string) {
    return this.rpc.call("ConfirmIndeterminateAction", ConfirmIndeterminateActionRequestSchema, ConfirmIndeterminateActionResponseSchema, {
      context: requestContext({ requestId }),
      workspace: workspaceRef(workspaceId),
      actionId,
      confirm,
      resolution,
    })
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

export class ConnectionClient {
  private readonly rpc: TypedConnectClient
  constructor(json: ConnectJsonClient) {
    this.rpc = new TypedConnectClient(json, "drift.v1.ConnectionService")
  }
  // connectEndpoint opens one transport. The serial travels with the endpoint
  // because it is what lets the service honour a port activation the operator
  // made for THAT device; without it the profile's accepted ports decide alone.
  connectEndpoint(requestId: string, serial: string, endpoint: string) {
    return this.rpc.call("ConnectEndpoint", ConnectEndpointRequestSchema, ConnectEndpointResponseSchema, {
      context: requestContext({ requestId }),
      serial,
      endpoint,
    })
  }
  // activateFleet moves every discovered device that is not already answering on
  // this port onto it. It carries the port and NOTHING else: the fleet is read
  // from the devices by the control plane, so a client cannot assert which
  // devices are attached, and the response answers for each serial on its own.
  activateFleet(requestId: string, port: number) {
    return this.rpc.call("ActivateFleet", ActivateFleetRequestSchema, ActivateFleetResponseSchema, {
      context: requestContext({ requestId }),
      port,
    })
  }
  restartServer(requestId: string, endpoints: readonly string[]) {
    return this.rpc.call("RestartServer", RestartServerRequestSchema, RestartServerResponseSchema, {
      context: requestContext({ requestId }),
      endpoints: [...endpoints],
    })
  }
}

export interface ControlPlaneServices {
  device: DeviceClient
  connection: ConnectionClient
  networkProfile: NetworkProfileClient
  discovery: DiscoveryClient
  group: GroupClient
  endpoint: EndpointClient
  observation: ObservationClient
  event: EventClient
  edgeAgent: EdgeAgentClient
  lease: LeaseClient
  action: ActionClient
  deviceInput: DeviceInputClient
  textReference: TextReferenceClient
  deviceSettings: DeviceSettingsClient
  deviceOperations: DeviceOperationsClient
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
  mirror: MirrorClient
  runtime: RuntimeClient
}

export function createControlPlaneServices(json: ConnectJsonClient): ControlPlaneServices {
  return {
    device: new DeviceClient(json),
    connection: new ConnectionClient(json),
    networkProfile: new NetworkProfileClient(json),
    discovery: new DiscoveryClient(json),
    group: new GroupClient(json),
    endpoint: new EndpointClient(json),
    observation: new ObservationClient(json),
    event: new EventClient(json),
    edgeAgent: new EdgeAgentClient(json),
    lease: new LeaseClient(json),
    action: new ActionClient(json),
    deviceInput: new DeviceInputClient(json),
    textReference: new TextReferenceClient(json),
    deviceSettings: new DeviceSettingsClient(json),
    deviceOperations: new DeviceOperationsClient(json),
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
    mirror: new MirrorClient(json),
    runtime: new RuntimeClient(json),
  }
}
