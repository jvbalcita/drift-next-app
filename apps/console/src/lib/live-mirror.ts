import { MirrorPreviewQuality, MirrorStreamState, MirrorTransport, MirrorViewerPurpose } from "@/gen/drift/v1/device_mirror_pb"
import type { MirrorCapacity, MirrorStream } from "@/gen/drift/v1/device_mirror_pb"
import { deviceObservationSentence } from "@/lib/device-status"
import type { DeviceStatus } from "@/lib/domain/control-plane"

/**
 * The console's own view of one device's live mirror.
 *
 * The big frame is the device an operator is working on, so what it renders is a
 * stream and what it sends back is input measured in that stream's own frame.
 * Both halves need one vocabulary for the stream's transport and state, and this
 * module is it: the surface, the session hook and their tests all read the same
 * table, so a state the control plane can report cannot render as an empty
 * string that reads like a working picture.
 */

/** The transport a stream IS using, as this console renders it. */
export type LiveMirrorTransport = "webrtc" | "tcp" | "unspecified"

/**
 * What the console is showing.
 *
 * `starting` and `live` are deliberately distinct: a connection that is up and
 * showing nothing is not a picture, and this fleet's encoder will happily produce
 * a connected-but-black stream with no error anywhere. `live` is reached only
 * when the control plane reports pictures carried, never from a peer connection.
 *
 * `unreadable` is the console's own state and NOT one the plane can report: it is
 * a read that could not be completed, which is a fact about this console's reach
 * and not about the stream. It exists because the two were conflated - a failed
 * read was reported as the stream failing, which on this plane is destructive
 * rather than merely wrong, since a session's last viewer detaching is what ENDS
 * the session. A stream whose state is no longer known is not live, so this
 * console stops claiming it is live; the stream itself is left open, its picture
 * is left in place, and the read is retried on a bounded backoff.
 */
export type LiveMirrorPhase = "idle" | "unavailable" | "opening" | "starting" | "live" | "unreadable" | "ended" | "failed"

/**
 * Whether this console is holding a stream open for what it renders.
 *
 * `starting`, `live` and `unreadable` are the three states in which a stream this
 * console opened is still open: nothing has been stopped and no picture has been
 * torn down. `unreadable` is in that set deliberately - a read this console could
 * not complete is not a report that the stream ended, so the picture the stream
 * is carrying stays where it is and the operator keeps working, while the state
 * line says the console cannot vouch for it.
 *
 * Everything that is about the STREAM rather than about the console's report of
 * it reads this one predicate: whether a picture is shown (the frame and its
 * tiles), whether input may be measured in it, and whether there is a stream for
 * the operator to stop. A set repeated three times is a set one of them gets
 * wrong.
 */
export function livePictureHeld(phase: LiveMirrorPhase): boolean {
  return phase === "starting" || phase === "live" || phase === "unreadable"
}

/** One device's live stream, in the console's own terms. */
export interface LiveStreamView {
  streamId: string
  deviceId: string
  transport: LiveMirrorTransport
  /** The size the stream is ENCODED at: the frame every coordinate is measured in. */
  renderWidth: number
  renderHeight: number
  state: "starting" | "live" | "ended" | "failed" | "unspecified"
  failure: string
  frames: number
  keyFrames: number
  /**
   * streamUrl is the per-device stream endpoint a TCP stream is fetched from, as
   * this service named it. It is empty for a stream carried over WebRTC, which is
   * negotiated instead.
   */
  streamUrl: string
}

/**
 * The transport an operator chooses in Console Settings.
 *
 * Both are carried, and the choice travels with every stream this console opens:
 * the control plane carries the transport that was asked for or refuses it, so a
 * stream never quietly arrives over the other one.
 */
export type LiveMirrorTransportChoice = "webrtc" | "tcp"

/**
 * The two facts a sentence about a device this console cannot mirror is read
 * from: the device's own name, so a frame that is one of several names the one it
 * is about, and the observation status the control plane last recorded for it,
 * which is the fact that decides whether a stream can be carried at all.
 *
 * The status is the console's own vocabulary (`@/lib/device-status`), so this
 * surface cannot call a device nobody has observed the same thing as one that was
 * observed and is not observed now (AGENTS.md section 2).
 */
export interface MirrorDevice {
  displayName: string
  status: DeviceStatus
}

export function transportRequestFor(choice: LiveMirrorTransportChoice): MirrorTransport {
  return choice === "tcp" ? MirrorTransport.TCP : MirrorTransport.WEBRTC
}

/**
 * The two things a viewer of a device's stream can be, as the control plane
 * counts them.
 *
 * The plane's device-session capacity is spent per VIEWER PURPOSE, and it keeps a
 * place of that capacity for the operator's own frame. A grid tile is `ambient`:
 * a picture and nothing else, which is what makes it the spendable kind. The big
 * frame an operator works a device from is `operator`, and it is the reason the
 * reserve exists. This console knows which of the two a stream is because it knows
 * what the operator clicked, so it states the purpose rather than letting the plane
 * guess.
 */
export type LiveMirrorViewerPurpose = "ambient" | "operator"

export function purposeRequestFor(purpose: LiveMirrorViewerPurpose): MirrorViewerPurpose {
  return purpose === "ambient" ? MirrorViewerPurpose.AMBIENT : MirrorViewerPurpose.OPERATOR
}

/**
 * The workspace's preview setting, as the console's own controls state it.
 *
 * It is the bound the plane applies to the grid's tiles, and it is stated on
 * every ambient stream rather than read from the plane, because the operator is
 * the one who chose it. `quality` is one of the levels the plane's table caps
 * (Low 480/0.5 Mbps, Medium 720/1.2 Mbps, High 1080/2.5 Mbps, Extra native/6
 * Mbps) and `frameRate` is the 1-24 fps the control offers.
 *
 * It is deliberately NOT applied to the operator's own big frame: that frame is
 * where the work happens and the plane carries it at its own profile whatever
 * this says, so a level chosen for a grid of thumbnails can never make it
 * blurry.
 */
export interface LiveMirrorPreview {
  quality: "Low" | "Medium" | "High" | "Extra"
  frameRate: number
}

/**
 * The two settings' wire form, as the request carries them.
 *
 * An operator's own frame states NOTHING rather than stating the workspace's
 * setting and relying on the plane to ignore it: the profile that frame is
 * carried at is the plane's own, and a request that stated a level the plane does
 * not apply would be this console claiming a bound it does not set.
 */
export function previewRequestFor(purpose: LiveMirrorViewerPurpose, preview?: LiveMirrorPreview): { previewQuality: MirrorPreviewQuality; frameRate: number } {
  if (purpose !== "ambient" || !preview) {
    return { previewQuality: MirrorPreviewQuality.UNSPECIFIED, frameRate: 0 }
  }
  return { previewQuality: previewQualityRequestFor(preview.quality), frameRate: preview.frameRate }
}

/**
 * previewQualityRequestFor maps a control's level onto the plane's vocabulary.
 *
 * An unrecognised level is UNSPECIFIED rather than an error: the plane answers an
 * unstated level with its own setting, which is itself a cap, so a level this
 * console cannot name is bounded rather than unbounded.
 */
export function previewQualityRequestFor(quality: LiveMirrorPreview["quality"]): MirrorPreviewQuality {
  switch (quality) {
    case "Low": return MirrorPreviewQuality.LOW
    case "Medium": return MirrorPreviewQuality.MEDIUM
    case "High": return MirrorPreviewQuality.HIGH
    case "Extra": return MirrorPreviewQuality.EXTRA
    default: return MirrorPreviewQuality.UNSPECIFIED
  }
}

/**
 * MirrorCapacityView is the plane's own device-session bound, as this console
 * reads it.
 *
 * `tilePlaces` is derived rather than read: it is the plane's capacity less the
 * place the plane keeps for the operator's own frame, which is the number of grid
 * tiles the plane can actually carry. A plane that stated a reserve larger than
 * its capacity offers the grid nothing, and this is where that reads as zero
 * places rather than as a negative number of tiles.
 */
export interface MirrorCapacityView {
  sessionCapacity: number
  operatorReserve: number
  tilePlaces: number
}

export function mirrorCapacityView(capacity: MirrorCapacity): MirrorCapacityView {
  const sessionCapacity = Math.max(0, capacity.sessionCapacity)
  const operatorReserve = Math.max(0, capacity.operatorReserve)
  return { sessionCapacity, operatorReserve, tilePlaces: Math.max(0, sessionCapacity - operatorReserve) }
}

export function liveTransportOf(transport: MirrorTransport): LiveMirrorTransport {
  switch (transport) {
    case MirrorTransport.WEBRTC: return "webrtc"
    case MirrorTransport.TCP: return "tcp"
    default: return "unspecified"
  }
}

export function liveStateOf(state: MirrorStreamState): LiveStreamView["state"] {
  switch (state) {
    case MirrorStreamState.STARTING: return "starting"
    case MirrorStreamState.LIVE: return "live"
    case MirrorStreamState.ENDED: return "ended"
    case MirrorStreamState.FAILED: return "failed"
    default: return "unspecified"
  }
}

export function liveStreamView(stream: MirrorStream): LiveStreamView {
  return {
    streamId: stream.streamId,
    deviceId: stream.deviceId,
    transport: liveTransportOf(stream.transport),
    renderWidth: stream.renderWidth,
    renderHeight: stream.renderHeight,
    state: liveStateOf(stream.state),
    failure: stream.failure,
    frames: Number(stream.frames),
    keyFrames: Number(stream.keyFrames),
    streamUrl: stream.streamUrl,
  }
}

/**
 * The render size a stream carries, or nothing.
 *
 * A frame needs both dimensions and both bounded: a zero renders as no frame at
 * all, and a surface that treated one as a frame would send coordinates measured
 * against nothing.
 */
export function liveStreamFrame(view: LiveStreamView | null): { width: number; height: number } | null {
  if (!view) return null
  if (!Number.isFinite(view.renderWidth) || !Number.isFinite(view.renderHeight)) return null
  if (view.renderWidth <= 0 || view.renderHeight <= 0) return null
  return { width: view.renderWidth, height: view.renderHeight }
}

/**
 * The observation a coordinate measured in this stream is bound to.
 *
 * It is the stream's own identity, because the stream IS the observation: an
 * operator points at a picture this stream carried, in the frame this stream is
 * encoded at, and the coordinate travels with that frame. It is also the session
 * the input travels on a mirrored device, so the token names the very thing that
 * carries the coordinate rather than a second fact about it. There is nothing to
 * look up and nothing to invent - a frame with no stream has no observation, and
 * this returns the empty string so the surface refuses and names it.
 *
 * It is deliberately NOT a captured observation's freshness token. The console
 * used to demand one, on the theory that a coordinate belongs to a capture; on a
 * real host that demand can never be met (nothing in the product records an
 * observation snapshot outside the lab adapter) and a live frame therefore
 * refused every click with an observation it could not have, while the plane
 * cross-checked nothing at all. Naming the stream is both the truthful answer and
 * the one the kernel can verify: the declared frame still has to match the size
 * the device presents at, and a frame it does not present at is still refused.
 */
export function streamObservationToken(view: LiveStreamView | null): string {
  if (!view) return ""
  return view.streamId.trim()
}

/**
 * The copy this feature renders.
 *
 * It lives in one place because the copy beside a control is part of the control
 * (AGENTS.md section 7): the sentence an operator reads and the test that pins it
 * move together, and the surface has no literal of its own to drift from this
 * table.
 */
export const liveMirrorCopy = {
  sectionLabel: "Live mirror",
  /** Which transport is in use, stated plainly rather than implied by a colour. */
  transport: {
    webrtc: "WebRTC (pion, inside the control plane)",
    tcp: "TCP (MSE over the control plane's stream surface)",
    /**
     * A stream the control plane carried without naming a transport for it.
     *
     * This is NOT "a transport this console does not recognise": this console
     * carries both transports a device is reached at, so what is missing is the
     * plane's own record of the transport, not console support for one.
     */
    unspecified: "no transport was reported for this stream",
  },
  /**
   * The transport line when this console has no stream for the device.
   *
   * There is no stream to state a transport for, and the fact that decides
   * whether one can be carried is the device's own observation — so the line
   * states that, in the status module's own words, instead of calling the
   * transport one this console does not recognise: an operator sent hunting for
   * console support the console already has never observes the device, and
   * observing it is the whole of the fix. The two not-observed states keep two
   * sentences and two actions, because they are two facts.
   */
  noStream: {
    unobserved: "No transport is recorded for it, so no live stream can be carried: observe it first, with a scan, which records the device and the transport it is reached at.",
    offline: "The transport it was last reached at is no longer current, so no live stream can be carried: observe it again, with a scan, which records the transport it is reached at now.",
    observed: "This console has no stream open for it, so there is no transport to state.",
  },
  /**
   * The layers this surface's two stacking decisions are stated in.
   *
   * They are one table because they are one relationship, and it is a
   * relationship that has already been got wrong: the info control's tooltip is
   * portaled to the document, so it leaves the floating device's stacking context
   * and is drawn in its own layer. At the console's standard overlay layer
   * (`z-50`) it was painted UNDERNEATH the floating device - which is above that
   * layer precisely because it floats over the whole workspace - so the sentence
   * describing the info control was unreadable exactly where the operator was
   * looking. These are the console's two highest layers, and the tooltip's is
   * above the frame's on purpose.
   */
  layers: {
    /** The floating device, above every overlay the console draws. */
    floatingFrame: "z-[100]",
    /** The floating device's own tooltip, above the surface it describes. */
    frameTooltip: "z-[110]",
  },
  /** The state line, per phase. */
  phase: {
    idle: "Choose a phone to open its live frame.",
    unavailable: "This console has no control plane behind it, so no device screen can be carried. Fixture data only.",
    opening: "Opening the live stream…",
    starting: "The stream is connected and has not carried a picture yet. A connected stream that shows nothing is not a working picture.",
    live: "Live.",
    /**
     * The console could not READ the plane, and it says exactly that.
     *
     * This sentence is not a failure of the stream and must never read as one: the
     * plane has said nothing about the stream, the console has no fact about it,
     * and the one thing it must not do is dress that up as either a working picture
     * or a dead one. It names what it cannot do, what it has NOT done (stopped
     * nothing, taken no picture down) and what it is doing instead, because an
     * operator who reads "not live" needs to know whether their picture was
     * destroyed by a two-second hiccup.
     */
    unreadable: "The control plane could not be read for this stream, so this console cannot say it is live. Nothing was stopped and no picture was taken down: the stream is left open and the read is retried on a bounded backoff until the control plane answers.",
    ended: "The stream ended. The frame you would see is the last one it carried, not the device's screen now.",
    failed: "The stream failed.",
  },
  /** Key input: the labels an operator reads, and the codes they dispatch. */
  /**
   * The panel footer is the DEVICE's own navigation bar, drawn as the phone's
   * bottom bar draws it: menu (the app switcher), home, back.
   *
   * The keys are the device's own key events - KEYCODE_APP_SWITCH, HOME and BACK
   * - dispatched through the kernel like every other input, so the row reaches
   * what the phone itself puts in that bar rather than a console's idea of what
   * an operator might want there. It is named `navigationKeys` rather than
   * `deviceKeys` because this module already holds the device's own key VOCABULARY
   * under that name (the keys the operator's own keyboard can send), and two
   * tables called the same thing is one table too many.
   *
   * The row is three controls because the device's bar is three controls. The key
   * row and the type-into-the-device field this replaces are gone: every other key
   * an operator might send travels the operator's own keyboard (see `capture`),
   * which is the surface that carded typing into a device.
   */
  navigationKeys: {
    label: "Device navigation",
    keys: [
      { name: "recents", label: "Menu (recent apps)", keyCode: 187 },
      { name: "home", label: "Home", keyCode: 3 },
      { name: "back", label: "Back", keyCode: 4 },
    ],
  },
  /**
   * The operator's own keyboard, as the info control states it.
   *
   * It is stated ONCE, in the details, and never in the panel: the panel's
   * capture block, its paragraph and its release button are gone, because the
   * behaviour they restated was never a control - the frame's focus IS the
   * capture boundary, and focus leaving the frame is what ends it. `frameLabel`
   * still names the frame itself, because the frame is the element an operator
   * focuses to type and a focusable element with no name names nothing.
   *
   * `note` states a FACT rather than a live reading of the state, and that is
   * deliberate rather than a loss: the control it is read from is one an operator
   * OPENS, and opening it takes focus off the frame - so a line that claimed
   * capture was on could never be read while it was true. What is true whichever
   * way it stands is how the keyboard reaches the device and how it is given
   * back, and the frame's own focus ring is the live state.
   */
  capture: {
    /** The frame's accessible name: the frame is where the operator's keyboard starts. */
    frameLabel: "Device screen. Click it, or focus it with Tab, to type into the device with your own keyboard.",
    /** How the operator's own keyboard works, said once, where a state readout could not be read. */
    note: "Your own keyboard reaches the device while this frame holds focus: click the device's screen, or focus it with Tab, to type into it. A keystroke travels the same lease, policy and control session as every other input, a key the contract cannot express is refused and named, and a key the control plane refuses is reported with its refusal. Focus leaving the frame is what ends capture, and Tab is dispatched too and moves focus out of the frame, so capture is leavable from the keyboard alone - there is no control to press to leave it.",
  },
  /** Why input cannot be sent, said before anything is dispatched. */
  input: {
    noLease: "Input needs this device's active lease, which this console has not acquired.",
    /**
     * Said when this frame has no live stream to measure a coordinate in.
     *
     * The observation a coordinate is measured from is the frame's own live
     * stream, so a frame with no stream has nothing to measure in and nothing to
     * bind a point to. It is a refusal and not a request to observe the device:
     * the stream is what the operator is looking at, and this sentence names it.
     */
    noObservation: "Input needs the observation the coordinates are measured from, which is this frame's own live stream, and this frame has no stream open.",
    noFrame: "Input needs the frame the stream is encoded at, and this stream has not reported one.",
    refused: "The control plane refused that input.",
    tapSent: "Tap dispatched.",
  },
  /**
   * The info control and everything the frame's own body no longer carries.
   *
   * The frame IS the device's screen: it holds the picture and the pointer and
   * nothing else, so the state, the transport, the encoded frame, the box the
   * picture is drawn in, and every refusal and warning are read from here
   * instead (AGENTS.md section 7 - the text beside a control is part of the
   * control). Nothing moved here is dropped: each sentence is the one the frame
   * used to print, and the tests that pinned it pin it here.
   */
  details: {
    /** The tooltip names what the control opens, and nothing else. */
    label: "Live mirror details",
    tooltip: "Live mirror details: what this frame is showing, the frame its coordinates are measured in, and every refusal it has reported.",
    heading: "Live mirror details",
    /** What this surface is, said once, so the lines below read as one account. */
    intro: "Everything this frame used to print over the device's screen. The frame itself is the device's screen: it carries the picture and the pointer, and nothing else.",
    /** The paragraph the frame's body used to carry under the picture. */
    pointer: "Tap the picture to tap the device, drag it to swipe, and scroll inside it to scroll the device. Coordinates are measured in the frame the stream is encoded at, never in this element's pixels and never rescaled from another one. The picture is drawn at the stream's own shape, so a point in the bar beside it reaches no device: it is refused and named rather than moved onto the frame.",
    /** The fact each line states, so a reader can tell one line's subject from another. */
    field: {
      state: "State",
      transport: "Transport",
      frame: "Encoded frame",
      observation: "Observation",
      drawn: "Drawn picture",
      pointer: "Pointer",
      /**
       * The operator's own keyboard, stated with the state it is in and with the
       * one way it is left.
       *
       * It is the line the panel's removed capture block used to carry. The
       * state had to stay readable - a state an operator cannot read is one they
       * will assume - so it moved here with the rest of what the frame's body no
       * longer prints, and it is stated once.
       */
      keyboard: "Keyboard",
      failure: "Failure reason",
      refusal: "Refusal",
      controlSession: "Control session",
      warning: "Input warning",
    },
    /**
     * The observation this frame's coordinates are measured from, stated with the
     * value that travels with them.
     *
     * It is the frame's own live stream, and the line says so rather than leaving
     * the operator to infer which of a device's many observations a click will be
     * tagged with: the value below is the one the kernel receives.
     */
    observationPresent: (token: string) => `This frame's live stream, ${token}: the observation every coordinate you point at here is measured from and dispatched against.`,
    observationAbsent: "This frame has no live stream, so there is no observation for a coordinate to be measured from and every input is refused until one is open.",
    /** The drawn box has no measurement to state yet, and says which fact is missing. */
    drawnUnmeasured: "No picture has been drawn from this stream yet, so there is no drawn box for a point to be measured in.",
    /**
     * The details are holding something an operator has not read.
     *
     * The info control marks itself with this rather than leaving a refusal
     * behind an unopened control: a refusal is still named, never silent, and
     * the frame's body stays the picture.
     */
    unread: "This frame has refused something. Open the details to read it.",
    /**
     * The mark's own sentence when what the frame is holding is a READ it could not
     * complete.
     *
     * It is a second sentence rather than the refusal one because it is a second
     * fact: nothing was refused and nothing failed - the console could not ask the
     * plane - and a mark that said "this frame has refused something" over a stream
     * that is still carrying pictures would name the wrong thing. This is the only
     * place the unreadable state is visible without opening the details, because it
     * takes neither the picture nor a control away with it.
     */
    unreadable: "This frame could not read the control plane, so it cannot say the stream is live. Open the details to read what it is showing.",
    waiting: "Nothing has been reported yet: this frame has no stream to state anything about.",
  },
  /** The sentences that stand in for a stream the console cannot show. */
  failure: {
    noStream: "The control plane answered without a stream, so there is nothing to show.",
    noCapacity: "The control plane answered without its live-stream capacity, so this console cannot tell how many pictures it may carry and is carrying none.",
    openFailed: "The live stream could not be opened.",
    /**
     * The identity this console held is not one the plane knows, and the plane
     * opened no other for the device.
     *
     * It is the ONE report this path makes, and a plane that RESTARTED never
     * reaches it: a re-open resolves to the session the plane already carries or
     * starts one, which is what re-entry is for. It is reached only by a plane that
     * hands out a stream identity and forgets it on every read, so it says what
     * actually happened rather than borrowing the words of a failure the plane
     * never reported.
     */
    unresumable: "The control plane no longer knows this stream and opened no other for the device, so this console is carrying no picture for it. The plane is the one that forgot the stream, and nothing here was stopped for a read this console could not complete.",
    noEndpoint: "The control plane opened a stream over the TCP transport and named no stream endpoint to fetch, so there is nothing to read.",
    refusedEndpoint: "The control plane refused the stream endpoint for this stream.",
    noInitSegment: "The stream carried a picture before the segment that describes its codec, so a decoder cannot be told what it is about to decode.",
    noCodec: "The stream's initialisation segment declares no H.264 codec, so there is nothing to hand a decoder.",
    sourceNeverOpened: "The browser's media source never opened, so this stream could not be handed to it.",
  },
  /**
   * What the console adds to a refusal, when the device it could not open a
   * stream for has no current observation.
   *
   * The control plane's own sentence is kept on the end of it rather than
   * replaced — a refusal is reported, never softened — and what is added is what
   * a sentence about a transport endpoint cannot carry: which device this frame
   * is about, and the action that records the observation it has none of.
   */
  openRefusal: {
    unobserved: "The control plane opened nothing for it: a device nobody has observed has no transport at which to carry a live stream. Observe it first with a scan, which records the device and the transport it is reached at.",
    offline: "The control plane opened nothing for it: the transport it was last reached at is no longer current, so there is none to carry a live stream to it. Observe it again with a scan, which records the transport it is reached at now.",
    unauthorized: "The control plane opened nothing for it: the device is attached and has not authorized this host, so there is no transport it may act over. No host can accept that prompt for the device — it has to be authorized once on its own display, or by placing this host's key on it.",
    no_permissions: "The control plane opened nothing for it: the device is attached and this host may not open it, so there is no transport it may act over. That one is fixed on this host rather than on the device.",
    answered: "The control plane answered:",
  },
  /** Why a coordinate never left the console. */
  refusal: {
    noSurface: "That point is not on the stream surface.",
    noFrame: "The stream has not reported the frame its coordinates are measured in.",
    noPicture: "The browser has not drawn a picture from this stream yet, so there is no frame on screen to point at.",
    outsideFrame: "That point is not on the frame the device presents at: the stream is drawn smaller than this element, and a point in the bar beside the picture reaches no device.",
    /**
     * A scroll whose gesture has no room left inside the frame.
     *
     * It is its own sentence because the alternative is worse: a gesture that
     * would leave the frame is refused rather than clipped into a shorter
     * movement at one edge only, and an operator who scrolled at the frame's
     * very edge is told that is what happened rather than watching nothing move.
     * The two axes are told apart so the sentence names the direction.
     */
    noScrollRoomY: "That scroll would end outside the frame the device presents at, and this console does not rescale a gesture: at this point on the picture there is no room to scroll vertically.",
    noScrollRoomX: "That scroll would end outside the frame the device presents at, and this console does not rescale a gesture: at this point on the picture there is no room to scroll horizontally.",
  },
  /**
   * The Console Settings entry. Acceptance criterion 1: the choice exists because
   * BOTH transports work. It is what an operator's streams are opened over, and
   * the surface states which one a stream is actually using.
   */
  settings: {
    label: "Live Mirror Transport",
    choice: {
      webrtc: "WebRTC (pion, inside the control plane)",
      tcp: "TCP (MSE, the service's stream endpoint)",
    } satisfies Record<LiveMirrorTransportChoice, string>,
    notice: "Both transports carry this console. WebRTC negotiates a peer connection and pushes the pictures to it; TCP fetches this device's own stream endpoint and plays it as MSE, which is the slower path and the one that works where WebRTC does not. The choice is sent with every stream this console opens.",
  },
} as const

/** The sentence for a phase, with the number of pictures a stream carried where it matters. */
export function livePhaseSentence(phase: LiveMirrorPhase, view: LiveStreamView | null): string {
  if (phase === "live") return `${liveMirrorCopy.phase.live} ${view ? `${view.frames} picture(s) carried` : ""}`.trim()
  if (phase === "ended") return `${liveMirrorCopy.phase.ended}${view ? ` ${view.frames} picture(s) were carried.` : ""}`
  return liveMirrorCopy.phase[phase]
}

/**
 * The transport sentence for a stream, or the reason there is no transport to
 * state.
 *
 * A device this console has no stream for has no transport to state, and saying
 * the transport is one the console does not recognise sends the operator looking
 * for console support the console already has: this console carries both
 * transports a device is reached at, and what is missing is the observation a
 * transport is recorded from. The device's own status is read for it (see
 * `absentTransportSentence`), so the line names the device and the action that
 * changes its answer.
 */
export function transportSentence(view: LiveStreamView | null, device: MirrorDevice): string {
  if (!view) return absentTransportSentence(device)
  return liveMirrorCopy.transport[view.transport]
}

/**
 * The transport line for a device this console has no stream for.
 *
 * It reads the observation status the control plane recorded, because that is the
 * fact that decides whether a stream can be carried: a device nobody has observed
 * has no transport recorded for it, while a device observed before and not
 * observed now has one that is no longer current. The two are kept apart in the
 * status module's own words (AGENTS.md section 2), and each names the action that
 * fixes it: a scan, which is what records an observation and the transport with
 * it. A device the control plane does observe gets a sentence that does not claim
 * otherwise, because the console having no stream is then the whole of the fact.
 */
export function absentTransportSentence(device: MirrorDevice): string {
  switch (device.status) {
    case "online":
    case "attention":
    // An attached-but-unauthorized device (and one this host may not open) IS
    // observed: the console having no stream is then the whole of the fact, so it
    // gets the sentence that does not claim otherwise (ARC-196).
    case "unauthorized":
    case "no_permissions":
      return `${deviceObservationSentence(device.displayName, device.status)} ${liveMirrorCopy.noStream.observed}`
    case "unobserved":
      return `${deviceObservationSentence(device.displayName, device.status)} ${liveMirrorCopy.noStream.unobserved}`
    case "offline":
      return `${deviceObservationSentence(device.displayName, device.status)} ${liveMirrorCopy.noStream.offline}`
    default: {
      const _exhaustive: never = device.status
      return _exhaustive
    }
  }
}

/**
 * The refusal an operator reads when the control plane would not carry a stream
 * for a device with no current observation.
 *
 * The control plane answers a sentence about a transport endpoint, which names
 * its own table and neither the device nor the operator's action; the console
 * knows which device this frame is about and what the plane last recorded for it,
 * so it states that in the status module's own words - the two not-observed states
 * are not one fact - and keeps the plane's answer verbatim on the end. The refusal
 * is thus reported rather than replaced or softened, and a device the plane does
 * observe keeps the plane's own sentence, because there is nothing the console
 * knows about that refusal which the plane's answer does not already say.
 */
export function refusedStreamSentence(device: MirrorDevice, planeReason: string): string {
  const reason = planeReason.trim() === "" ? liveMirrorCopy.failure.openFailed : planeReason.trim()
  switch (device.status) {
    case "online":
    case "attention":
      return reason
    case "unauthorized":
      return `${deviceObservationSentence(device.displayName, device.status)} ${liveMirrorCopy.openRefusal.unauthorized} ${liveMirrorCopy.openRefusal.answered} ${reason}`
    case "no_permissions":
      return `${deviceObservationSentence(device.displayName, device.status)} ${liveMirrorCopy.openRefusal.no_permissions} ${liveMirrorCopy.openRefusal.answered} ${reason}`
    case "unobserved":
      return `${deviceObservationSentence(device.displayName, device.status)} ${liveMirrorCopy.openRefusal.unobserved} ${liveMirrorCopy.openRefusal.answered} ${reason}`
    case "offline":
      return `${deviceObservationSentence(device.displayName, device.status)} ${liveMirrorCopy.openRefusal.offline} ${liveMirrorCopy.openRefusal.answered} ${reason}`
    default: {
      const _exhaustive: never = device.status
      return _exhaustive
    }
  }
}

/**
 * A point on the rendered surface, in the frame the STREAM is encoded at.
 *
 * The element an operator points at is the stream drawn at some other size, so a
 * point has to be mapped back into the stream's own frame before it is sent: the
 * device's coordinates are the encoded frame's, and a coordinate from another
 * frame is refused rather than converted (AGENTS.md section 3).
 *
 * The rectangle this maps through is the one the PICTURE is drawn in, never the
 * element that holds it (see `drawnContentRect`). The element's box is the
 * operator's own layout, and this fleet's streams are not that shape: the
 * browser letterboxes or pillarboxes the picture inside the element and paints
 * nothing in the bars. A tap in a bar is a point on the element and not on the
 * device's screen, so it is refused rather than scaled onto the frame - the same
 * rule that already holds for a coordinate outside the frame.
 *
 * Three refusals are explicit rather than silent: a surface with no measurable
 * geometry, a stream that has reported no frame, and a picture the browser has
 * not drawn. None falls back to a default, because a default frame is a
 * coordinate nobody measured.
 *
 * The one adjustment made here is at the boundary: a point on the picture's own
 * far edge can map one pixel past the last column, which is the same mapping's
 * own rounding, so it is clamped into the frame instead of being refused. A
 * point outside the picture is not mapped at all.
 */
export type FramePoint =
  | { ok: true; x: number; y: number }
  | { ok: false; refusal: string }

export interface SurfaceRect {
  left: number
  top: number
  width: number
  height: number
}

export interface StreamFrame {
  width: number
  height: number
}

/**
 * An element's box, and the size of the picture drawn inside it.
 *
 * Both are DOM facts read at the moment a pointer event is handled, and each is
 * unusable without the other: a box with no measurable geometry cannot say where
 * the picture is, and a picture whose own size the browser has not reported
 * (`videoWidth`/`videoHeight` are zero until it has decoded one) has not been
 * drawn anywhere.
 */
export interface DrawnPicture {
  box: SurfaceRect
  content: StreamFrame
}

/**
 * The rectangle an `object-fit: contain` picture is actually painted in.
 *
 * The big frame is a box this console's own layout pins, and the stream a device
 * carries need not be that shape, so the browser fits the picture inside the
 * element - centred, whole, at the content's own aspect ratio - and paints
 * nothing in the bars left over. Those bars are not part of the device's screen,
 * so they are not part of any coordinate either: this rectangle is the mapping
 * origin.
 *
 * It returns null when either half is missing or unmeasurable, so a caller
 * refuses the point rather than placing the picture somewhere nobody observed.
 */
export function drawnContentRect(box: SurfaceRect | null, content: StreamFrame | null): SurfaceRect | null {
  if (!measurable(box) || !sized(content)) return null
  const scale = Math.min(box.width / content.width, box.height / content.height)
  const width = content.width * scale
  const height = content.height * scale
  return { left: box.left + (box.width - width) / 2, top: box.top + (box.height - height) / 2, width, height }
}

export function streamPoint(picture: DrawnPicture | null, frame: StreamFrame | null, clientX: number, clientY: number): FramePoint {
  if (!sized(frame)) return { ok: false, refusal: liveMirrorCopy.refusal.noFrame }
  if (!picture || !measurable(picture.box)) return { ok: false, refusal: liveMirrorCopy.refusal.noSurface }
  if (!Number.isFinite(clientX) || !Number.isFinite(clientY)) return { ok: false, refusal: liveMirrorCopy.refusal.noSurface }
  const drawn = drawnContentRect(picture.box, picture.content)
  if (!drawn) return { ok: false, refusal: liveMirrorCopy.refusal.noPicture }
  if (clientX < drawn.left || clientY < drawn.top || clientX > drawn.left + drawn.width || clientY > drawn.top + drawn.height) {
    return { ok: false, refusal: liveMirrorCopy.refusal.outsideFrame }
  }
  const x = Math.floor(((clientX - drawn.left) * frame.width) / drawn.width)
  const y = Math.floor(((clientY - drawn.top) * frame.height) / drawn.height)
  return { ok: true, x: Math.min(x, frame.width - 1), y: Math.min(y, frame.height - 1) }
}

function measurable(rect: SurfaceRect | null | undefined): rect is SurfaceRect {
  return !!rect && Number.isFinite(rect.left) && Number.isFinite(rect.top)
    && Number.isFinite(rect.width) && Number.isFinite(rect.height) && rect.width > 0 && rect.height > 0
}

function sized(frame: StreamFrame | null | undefined): frame is StreamFrame {
  return !!frame && Number.isFinite(frame.width) && Number.isFinite(frame.height) && frame.width > 0 && frame.height > 0
}

/**
 * The threshold that separates a tap from a swipe, in the frame's own units.
 *
 * The threshold is a property of the operator's hand, so it is expressed in the
 * rendered pixels a hand moves across and converted into the frame the stream is
 * encoded at. The rect it is converted against is the picture's own drawn box
 * (see `drawnContentRect`), which is the scale a finger actually moved at:
 * converting against the element's box would report a threshold the hand never
 * crossed, by exactly the letterbox's width.
 */
export const gestureThresholdPixels = 12
export const minimumSwipeMs = 16
export const maximumSwipeMs = 10_000

export function gestureThresholdFor(rect: SurfaceRect | null, frame: StreamFrame): number {
  if (!rect || rect.width <= 0) return gestureThresholdPixels
  return Math.max(1, Math.round((gestureThresholdPixels * frame.width) / rect.width))
}

export interface PointerSample {
  x: number
  y: number
  atMs: number
}

export interface GestureSamples {
  /** Where the press landed. */
  down: PointerSample
  /** The last point the pointer was at before it was released. */
  last: PointerSample
  /** When the pointer was released: the gesture's duration is measured down→release. */
  releasedAtMs: number
}

export type GesturePlan =
  | { kind: "tap"; x: number; y: number }
  | { kind: "swipe"; startX: number; startY: number; endX: number; endY: number; durationMs: number }
  | { kind: "refused"; refusal: string }

/**
 * One gesture in, exactly one action out.
 *
 * This is where intermediate touch points are compressed: everything between the
 * press and the release only moves the gesture's end point forward, and the plan
 * is read once, at the release. Nothing here streams a point per pointermove, so
 * a drag across the frame is one swipe to the device rather than the dozens of
 * intermediate points the browser handed us.
 */
export function planGesture(samples: GestureSamples, frame: StreamFrame, threshold: number): GesturePlan {
  const { down, last, releasedAtMs } = samples
  if (!insideFrame(down, frame) || !insideFrame(last, frame)) {
    return { kind: "refused", refusal: liveMirrorCopy.refusal.outsideFrame }
  }
  const durationMs = Math.min(Math.max(Math.round(releasedAtMs - down.atMs), minimumSwipeMs), maximumSwipeMs)
  const moved = Math.hypot(last.x - down.x, last.y - down.y)
  if (moved <= threshold) return { kind: "tap", x: down.x, y: down.y }
  return { kind: "swipe", startX: down.x, startY: down.y, endX: last.x, endY: last.y, durationMs }
}

function insideFrame(point: PointerSample, frame: StreamFrame): boolean {
  return Number.isFinite(point.x) && Number.isFinite(point.y) && point.x >= 0 && point.y >= 0 && point.x < frame.width && point.y < frame.height
}

/**
 * The keys this console can name in the device's own vocabulary.
 *
 * A key event carries ONE key code (`KeyEventInput`, ADR-0008), so every entry
 * here is a single key the device's input path already knows: the named keys an
 * operator reaches for while working a screen, the characters their own keyboard
 * types as themselves, and the four modifier keys. The codes are the device's own
 * (Android's key codes), and this table is the whole of what this console sends:
 * a key that is not in it is refused by name rather than mapped to a code nobody
 * checked, because a made-up code is a key event the device acts on.
 */
const deviceKeys: Record<string, { keyCode: number; label: string; modifier?: boolean }> = {
  Enter: { keyCode: 66, label: "Enter" },
  Backspace: { keyCode: 67, label: "Backspace" },
  Delete: { keyCode: 112, label: "Delete" },
  Tab: { keyCode: 61, label: "Tab" },
  Escape: { keyCode: 111, label: "Escape" },
  ArrowUp: { keyCode: 19, label: "Arrow up" },
  ArrowDown: { keyCode: 20, label: "Arrow down" },
  ArrowLeft: { keyCode: 21, label: "Arrow left" },
  ArrowRight: { keyCode: 22, label: "Arrow right" },
  Home: { keyCode: 3, label: "Home" },
  End: { keyCode: 123, label: "End" },
  PageUp: { keyCode: 92, label: "Page up" },
  PageDown: { keyCode: 93, label: "Page down" },
  " ": { keyCode: 62, label: "Space" },
  Shift: { keyCode: 59, label: "Shift", modifier: true },
  Control: { keyCode: 113, label: "Control", modifier: true },
  Alt: { keyCode: 57, label: "Alt", modifier: true },
  Meta: { keyCode: 117, label: "Meta", modifier: true },
}

/** The device key code of `A`, which the rest of the alphabet follows. */
const letterAKeyCode = 29
/** The device key code of `0`, which the rest of the digits follow. */
const digitZeroKeyCode = 7

/** The four modifiers a browser reports beside a key, under the device's own name for each. */
const modifierKeys = [
  ["shiftKey", "Shift"],
  ["ctrlKey", "Control"],
  ["altKey", "Alt"],
  ["metaKey", "Meta"],
] as const

/**
 * One keystroke, as the browser reported it.
 *
 * `key` is what the operator's layout produced — the character for a character
 * key, and the key's own name for the rest — and the four flags are the modifiers
 * that were held when it did.
 */
export interface KeystrokeSample {
  key: string
  shiftKey: boolean
  ctrlKey: boolean
  altKey: boolean
  metaKey: boolean
}

/**
 * The key event a keystroke is, or the reason there is none.
 *
 * The console names the KEY and lets the device's own keyboard decide what that
 * key produces, which is why the character is matched whatever its case: an
 * operator with caps lock on pressed the same key as one without it. What the
 * flags are read for is the case the one-key-code contract cannot carry — a
 * modifier held at the same time is a second key, and `KeyEventInput` has one
 * `key_code` and no meta state, so the combination is refused and NAMED rather
 * than sent as the bare key, which would type something the operator did not
 * press. The modifier keys themselves are in the vocabulary and are sent as
 * their own event when one is held on its own.
 */
export type KeystrokePlan =
  | { kind: "key"; keyCode: number; label: string }
  | { kind: "unsupported"; refusal: string }

export function planKeystroke(sample: KeystrokeSample): KeystrokePlan {
  const named = deviceKeys[sample.key]
  const held = modifierKeys.filter(([flag]) => sample[flag]).map(([, name]) => name)
  if (named?.modifier) {
    if (held.length > 1) return { kind: "unsupported", refusal: keystrokeCombinationRefusal(held.join("+"), named.label) }
    return { kind: "key", keyCode: named.keyCode, label: named.label }
  }
  if (held.length > 0) return { kind: "unsupported", refusal: keystrokeCombinationRefusal(held.join("+"), keystrokeName(sample.key)) }
  if (named) return { kind: "key", keyCode: named.keyCode, label: named.label }
  const character = sample.key.length === 1 ? sample.key.toLowerCase() : ""
  if (character >= "a" && character <= "z") return { kind: "key", keyCode: letterAKeyCode + character.charCodeAt(0) - 97, label: character.toUpperCase() }
  if (character >= "0" && character <= "9") return { kind: "key", keyCode: digitZeroKeyCode + Number(character), label: character }
  return { kind: "unsupported", refusal: keystrokeUnknownRefusal(keystrokeName(sample.key)) }
}

/** keystrokeName names a key the way an operator would read it, for a refusal sentence. */
function keystrokeName(key: string): string {
  if (key === " ") return "Space"
  if (key.trim() === "") return "that key"
  return key
}

/**
 * The refusal an operator reads when a modifier is held with another key.
 *
 * It names the operator's own keystroke, so the sentence is about what they just
 * pressed rather than about a key this console offers somewhere: the console
 * cannot express it as one key event, and it says so instead of dropping it.
 * Typing the text is named as the way to get the operator's intent to the
 * device, because a character is what the typed-text kind carries.
 */
export function keystrokeCombinationRefusal(modifier: string, key: string): string {
  return `${modifier} held with ${key} is not one key event: the device input contract carries one key code per event and no modifier state, so this console cannot express it. Nothing was sent to the device. Release the modifier and press the key on its own, or type the text with the typed-text control.`
}

/** The refusal an operator reads when the device's vocabulary has no code for the key. */
export function keystrokeUnknownRefusal(key: string): string {
  return `${key} is not a key this console can name in the device's own key vocabulary, so nothing was sent to the device.`
}

/**
 * How often one held key may reach the device, in milliseconds.
 *
 * This is the rate the console gives the control session it holds for this frame,
 * and it is deliberately not the browser's: auto-repeat is emitted at whatever
 * rate the operator's own machine is configured for, so a frame that dispatched
 * every repeat would hand the device a burst whose size depends on a setting on
 * someone else's keyboard. Repeats that fall inside this interval are dropped
 * rather than queued — a queued repeat is a movement the operator's hand did not
 * make, delivered after they made it — and a burst the kernel refuses is reported
 * as that refusal, so nothing is lost silently.
 */
export const keyRepeatIntervalMs = 50

/** repeatDue answers whether a held key has waited its session's rate since the last one. */
export function repeatDue(lastDispatchAtMs: number | undefined, atMs: number): boolean {
  if (lastDispatchAtMs === undefined) return true
  if (!Number.isFinite(lastDispatchAtMs) || !Number.isFinite(atMs)) return false
  return atMs - lastDispatchAtMs >= keyRepeatIntervalMs
}

/**
 * One wheel turn, as the browser reported it.
 *
 * `deltaMode` is the browser's own contract for what the deltas are measured in:
 * 0 is pixels, 1 is lines (a mouse wheel's notch in Firefox), 2 is pages. All
 * three are converted before anything is measured, because a delta read as the
 * wrong unit is a scroll of the wrong size rather than a missing one.
 */
export interface WheelTurn {
  deltaX: number
  deltaY: number
  deltaMode: number
}

/** How many pixels one line of a deltaMode 1 wheel turn is worth. */
export const wheelLinePixels = 16

/** The span one scroll step is dispatched as, as a share of the frame's own dimension. */
export const scrollStepFraction = 1 / 12

/** How long one dispatched scroll swipe is given, in milliseconds. */
export const scrollSwipeMs = 80

/** The most scroll steps one wheel event may become, so a burst cannot fan out without bound. */
export const maximumScrollStepsPerEvent = 4

/** A scroll in the frame's own units. */
export interface FrameScroll {
  x: number
  y: number
}

/**
 * wheelScrollDelta converts one wheel turn into the scroll it asks for, in the
 * frame the stream is encoded at.
 *
 * It is measured through the box the picture is DRAWN in, exactly as a pointer
 * is: the element's own pixels are the operator's layout, and a scroll measured
 * in them would move the device by a distance that depends on the size of the
 * operator's window rather than on the device's screen. It returns null when
 * there is no drawn box to measure through, so the caller refuses rather than
 * converting against a box nobody has.
 */
export function wheelScrollDelta(turn: WheelTurn, drawn: SurfaceRect | null, frame: StreamFrame): FrameScroll | null {
  if (!sized(frame) || !measurable(drawn)) return null
  if (!Number.isFinite(turn.deltaX) || !Number.isFinite(turn.deltaY)) return null
  const pixelsX = wheelPixels(turn.deltaX, turn.deltaMode, drawn.width)
  const pixelsY = wheelPixels(turn.deltaY, turn.deltaMode, drawn.height)
  return {
    x: (pixelsX * frame.width) / drawn.width,
    y: (pixelsY * frame.height) / drawn.height,
  }
}

/** wheelPixels converts one delta to the element's pixels, per the wheel's own deltaMode. */
function wheelPixels(delta: number, deltaMode: number, page: number): number {
  if (deltaMode === 1) return delta * wheelLinePixels
  if (deltaMode === 2) return delta * page
  return delta
}

/** scrollStepUnits is the frame-unit distance one scroll step moves the device. */
export function scrollStepUnits(frame: StreamFrame, axis: "x" | "y"): number {
  const dimension = axis === "x" ? frame.width : frame.height
  return Math.max(1, Math.round(dimension * scrollStepFraction))
}

export interface ScrollSwipe {
  startX: number
  startY: number
  endX: number
  endY: number
  durationMs: number
}

export interface ScrollPlan {
  /** The gestures this call is worth, in the order they are dispatched. */
  swipes: ScrollSwipe[]
  /** The scroll not yet dispatched, carried to the next turn. */
  remainder: FrameScroll
  /** A scroll that cannot be expressed as a gesture inside the frame, named. */
  refusal: string
}

/**
 * planWheelScrolls turns accumulated wheel scroll into the swipes it is worth.
 *
 * The device input contract has no scroll kind: tap, swipe, typed text, key
 * event and app launch are what a device can be told (ADR-0008), so a wheel is
 * dispatched as the gesture a finger would make - a swipe in the scrolled
 * direction - through the same path, the same lease and the same frame as every
 * other gesture. Nothing here invents an input the kernel cannot authorize.
 *
 * One wheel turn is one gesture, and the scroll is quantised by the frame: a
 * trackpad reports a flick as tens of small turns, and dispatching one action
 * per turn would send tens of device commands for one movement of a hand. The
 * scroll is therefore accumulated and spent in whole steps of the frame's own
 * size; a remainder smaller than a step waits for the next turn, and whatever is
 * still pending when the operator stops is never dispatched - which is stated
 * rather than hidden: the console reports whole steps, not the event rate of the
 * pointing device.
 *
 * A gesture must lie inside the frame the device presents at (ADR-0010), so a
 * step at the frame's edge is shortened to fit rather than rescaled, and a step
 * with no room at all is refused with its own sentence.
 */
export function planWheelScrolls(at: { x: number; y: number }, scroll: FrameScroll, frame: StreamFrame): ScrollPlan {
  let remainder = { x: scroll.x, y: scroll.y }
  const swipes: ScrollSwipe[] = []
  let refusal = ""
  for (const axis of ["x", "y"] as const) {
    const step = scrollStepUnits(frame, axis)
    while (Math.abs(remainder[axis]) >= step && swipes.length < maximumScrollStepsPerEvent) {
      const direction = Math.sign(remainder[axis])
      const plan = scrollSwipe(at, axis, direction * step, frame)
      if (plan.kind === "refused") return { swipes, remainder: { x: 0, y: 0 }, refusal: plan.refusal }
      swipes.push(plan.swipe)
      remainder = { ...remainder, [axis]: remainder[axis] - direction * step }
    }
  }
  return { swipes, remainder, refusal }
}

/**
 * scrollSwipe is the gesture one scroll step becomes: from the point the wheel
 * is over, in the direction the content moves, bounded by the frame.
 *
 * The content moves opposite to the finger that scrolls it, which is why the end
 * point is the start MINUS the scroll rather than plus it: a wheel turned down
 * moves the content up, and the gesture that does that on a touchscreen drags
 * upward.
 */
function scrollSwipe(at: { x: number; y: number }, axis: "x" | "y", delta: number, frame: StreamFrame): { kind: "swipe"; swipe: ScrollSwipe } | { kind: "refused"; refusal: string } {
  const startX = clampIndex(at.x, frame.width)
  const startY = clampIndex(at.y, frame.height)
  const endX = axis === "x" ? clampIndex(startX - delta, frame.width) : startX
  const endY = axis === "y" ? clampIndex(startY - delta, frame.height) : startY
  if (endX === startX && endY === startY) {
    return { kind: "refused", refusal: axis === "x" ? liveMirrorCopy.refusal.noScrollRoomX : liveMirrorCopy.refusal.noScrollRoomY }
  }
  return { kind: "swipe", swipe: { startX, startY, endX, endY, durationMs: scrollSwipeMs } }
}

function clampIndex(value: number, dimension: number): number {
  if (!Number.isFinite(value)) return 0
  return Math.min(Math.max(Math.round(value), 0), Math.max(0, dimension - 1))
}
