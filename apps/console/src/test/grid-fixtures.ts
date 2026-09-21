import { create } from "@bufbuild/protobuf"
import type { MessageInitShape } from "@bufbuild/protobuf"
import { GridPreviewProfileSchema, GridStillLevel, GridStillSchema, GridStillState, SyncGridPreviewsResponseSchema, type GridPreviewProfile, type SyncGridPreviewsResponse } from "@/gen/drift/v1/grid_preview_pb"
import type { GridPreviewClient } from "@/lib/api/control-plane-clients"
import type { GridProfileView, GridStillStateName } from "@/lib/grid-stills"

/**
 * The fleet grid's still previews, as a case states them.
 *
 * What a case needs to be able to say is which device the plane holds a CURRENT
 * still for, which device's capture failed, which device its sweep did not reach,
 * and what the plane published about its own cost - so this module states exactly
 * those four things and builds the wire answer from them.
 *
 * The defaults are the deployment's own: the plane carries stills at MEDIUM
 * (360 px, JPEG quality 65), captures every four seconds and admits 64 devices to
 * one sweep. A case that wants a different plane states the difference.
 */

/** gridStillBytes is the picture a current still carries: the JPEG magic number, base64-encoded. */
export const gridStillBytes = "/9j/4AAQSkZJRg=="

type StillInit = Omit<MessageInitShape<typeof GridStillSchema>, "deviceId" | "$typeName">

/** currentStillInit is a still the plane captured and still calls current, with the picture a tile paints. */
export function currentStillInit(options: { capturedAtMs?: number; base64?: string; width?: number; height?: number } = {}): StillInit {
  const base64 = options.base64 ?? gridStillBytes
  return {
    state: GridStillState.CURRENT,
    capturedAt: new Date(options.capturedAtMs ?? Date.now()).toISOString(),
    contentHash: "sha256:4b1e",
    mediaType: "image/jpeg",
    stillBase64: base64,
    width: options.width ?? 360,
    height: options.height ?? 640,
    bytes: base64.length,
    sourceBytes: base64.length * 8,
    frames: 1,
  }
}

/** gridProfile is what this plane publishes about its grid's cost, as the console reads it. */
export function gridProfile(overrides: Partial<GridProfileView> = {}): GridProfileView {
  return {
    cadenceMillis: 4_000,
    level: "medium",
    levelMaxWidth: 360,
    levelJpegQuality: 65,
    stillByteBound: 80_000,
    maxDevices: 64,
    subscribed: 0,
    ...overrides,
  }
}

const levelOf = { unspecified: GridStillLevel.UNSPECIFIED, low: GridStillLevel.LOW, medium: GridStillLevel.MEDIUM, high: GridStillLevel.HIGH } as const

/** gridProfileProto publishes that profile in the plane's own terms. */
export function gridProfileProto(profile: GridProfileView = gridProfile()): GridPreviewProfile {
  return create(GridPreviewProfileSchema, {
    cadenceMillis: profile.cadenceMillis,
    level: levelOf[profile.level],
    levelMaxWidth: profile.levelMaxWidth,
    levelJpegQuality: profile.levelJpegQuality,
    stillByteBound: profile.stillByteBound,
    maxDevices: profile.maxDevices,
    subscribed: profile.subscribed,
  })
}

/** What one device's still IS in the answer a case is building. */
export interface GridStillCase {
  state?: GridStillStateName
  /** failureClass and failureDetail are the plane's own most recent capture failure. */
  failureClass?: string
  failureDetail?: string
  /** capturedAtMs is when the plane's last successful capture ran. */
  capturedAtMs?: number
  /** base64 is the picture a current still carries. */
  base64?: string
}

/**
 * gridAnswer builds the response this plane answers with for a set of devices.
 *
 * Every named device is answered - that is the plane's own rule, because a device
 * left out of an answer is a tile that can say nothing about it - so a case states
 * what each device's still IS rather than which devices to include. A device with
 * no case stated is answered the way this plane answers a device it cannot capture:
 * UNAVAILABLE, with the plane's own class and reason.
 */
export function gridAnswer(devices: Record<string, GridStillCase>, options: { refused?: readonly string[]; profile?: GridPreviewProfile } = {}): SyncGridPreviewsResponse {
  const stills = Object.entries(devices).map(([deviceId, state]) => ({ deviceId, ...stillInit(state) }))
  return create(SyncGridPreviewsResponseSchema, { stills, profile: options.profile ?? gridProfileProto(), refusedDeviceIds: [...(options.refused ?? [])] })
}

function stillInit(device: GridStillCase): StillInit {
  if ((device.state ?? "unavailable") === "current") return { ...currentStillInit({ capturedAtMs: device.capturedAtMs, base64: device.base64 }), failureClass: device.failureClass ?? "", failureDetail: device.failureDetail ?? "" }
  if (device.state === "pending") return { state: GridStillState.PENDING }
  if (device.state === "stale") {
    return {
      state: GridStillState.STALE,
      capturedAt: new Date(device.capturedAtMs ?? Date.now() - 30_000).toISOString(),
      contentHash: "sha256:9f02",
      frames: 4,
      failures: 1,
      failureClass: device.failureClass ?? "observation",
      failureDetail: device.failureDetail ?? "the capture path could not read this device's screen",
      // A stale still carries NO picture, which is the plane's own rule rather than
      // an omission: the last capture is not this device's screen now.
      mediaType: "image/jpeg",
      stillBase64: "",
    }
  }
  return {
    state: GridStillState.UNAVAILABLE,
    failureClass: device.failureClass ?? "observation",
    failureDetail: device.failureDetail ?? "the plane holds no transport for this device that a still may be captured from",
  }
}

export interface FakeGridPlane {
  client: GridPreviewClient
  /** requests are the device id lists this console named, one entry per reconciliation. */
  requests: string[][]
  /** stops are the workspaces the console released its capture set for. */
  stops: string[]
  /** answer replaces what the plane answers with, in place of the case-built one. */
  answer(next: SyncGridPreviewsResponse): void
  /** fail makes every following read fail, which is a read the console could not complete rather than a report about a device. */
  fail(failure: unknown | null): void
}

/**
 * fakeGridPlane is the plane's grid surface as a case drives it: it answers the
 * stills the case stated, records what the console named, and can be made to fail
 * a read.
 */
export function fakeGridPlane(options: { stills?: Record<string, GridStillCase>; refused?: readonly string[]; profile?: GridPreviewProfile } = {}): FakeGridPlane {
  const requests: string[][] = []
  const stops: string[] = []
  let stated = options.stills ?? {}
  let refusal: unknown | null = null
  let replaced: SyncGridPreviewsResponse | null = null
  const client: GridPreviewClient = {
    async syncGridPreviews(request) {
      requests.push([...request.deviceIds])
      if (refusal) throw refusal
      if (replaced) return replaced
      // The plane answers every device it was given, in the order it was given them,
      // EXCEPT the ones its own sweep bound refused: those it names in
      // refusedDeviceIds and sends no still for, because nothing was captured.
      const refused = new Set(options.refused ?? [])
      const devices: Record<string, GridStillCase> = {}
      for (const deviceId of request.deviceIds) if (!refused.has(deviceId)) devices[deviceId] = stated[deviceId] ?? {}
      return gridAnswer(devices, { refused: options.refused, profile: options.profile })
    },
    async stopGridPreviews(workspaceId) {
      stops.push(workspaceId)
      return 0
    },
  }
  return {
    client,
    requests,
    stops,
    answer(next) { replaced = next },
    fail(failure) { refusal = failure },
  }
}
