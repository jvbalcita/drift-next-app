# Big-frame control panel: the per-command decision

**Card:** ARC-138 — "Big-frame control panel: make every command actually work, per device".
**Surface:** the action column of the big (floating) device frame, `apps/console/src/pages/ControlPage.tsx`.
**Rule being applied:** AGENTS.md section 7 — *"Render a control only when it performs its action: it either runs the operation and reports the true outcome, or it is not shown."*
**Premises re-derived on:** 2026-09-21, from the current rules, not from ARC-138's original quote of `handleDeviceAction`. AGENTS.md section 3 retired the blanket ban on a general device command on 2026-09-17, so no control on this panel is refused *because it is a device command*. Every deferral below names the capability that is actually missing.

The thirteen controls the panel rendered, with the decision this card records for each. A control is **WIRED** only when it dispatches a typed action through the lease/fencing/policy/control-session kernel for the selected device and reports the kernel's own outcome. Everything not wired is **NOT RENDERED**, so the panel carries no control whose click is the end of the interaction.

| Control | Decision | Typed action it dispatches | Postcondition | Its own refusal |
|---|---|---|---|---|
| Change Device | **WIRED** — panel navigation, not a device command | `endDeviceControl(current)` then `beginDeviceControl(target)`; the frame's control session moves to the chosen device and the follower selection ends | the frame holds a control session and lease for the chosen device, or the plane's refusal names why not | the plane's own sentence for the begin, held where the frame already reads it |
| Volume Up | **WIRED** | `submitDeviceKeyEvent` keyCode `24` (`KEYCODE_VOLUME_UP`) | the catalog's key-event postcondition: a fresh observation shows the effect of the delivered key event | this frame's own reason when it cannot name the observation a key event requires; otherwise the kernel's |
| Volume Down | **WIRED** | `submitDeviceKeyEvent` keyCode `25` (`KEYCODE_VOLUME_DOWN`) | as above | as above |
| Power Button | **WIRED** | `submitDeviceKeyEvent` keyCode `26` (`KEYCODE_POWER`) | as above | as above |
| Screenshot | **WIRED** (already, unchanged) | `submitDeviceAction` kind `capture` through the kernel, then `captureLabObservation` for the serial the device is observed at | an artifact is written atomically and referenced by the observation that produced it | "This device has no single current transport endpoint to observe." |
| Lock Rotate | **NOT RENDERED** — needs a per-device catalogued-settings dispatch surface | none yet | the device's own read-back shows `accelerometer_rotation=0` and `user_rotation=0` | — |
| Reboot | **NOT RENDERED** — needs a new catalogued lifecycle kind | none yet | a departure and a return this plane observes through the transport watcher, not a synchronous read-back | — |
| Switch Keyboard | **NOT RENDERED** — needs a new catalogued operation and an owner ruling on which IME | none yet | — | — |
| Install APK | **NOT RENDERED** — needs a device push path and an admitted destination set | none yet | — | — |
| Import File | **NOT RENDERED** — as Install APK | none yet | — | — |
| Export File | **NOT RENDERED** — as Install APK, device → artifact store | none yet | — | — |
| ADB Command | **NOT RENDERED** — the ADVANCED form, whose catalogue the owner approves | none yet | — | — |
| Quick Phrase | **NOT RENDERED** — needs a stored phrase catalogue | none yet | — | — |

## The four wired commands

### Why key events, and not a new kind

`Volume Up`, `Volume Down` and `Power Button` are the device's own key events. The kernel already admits them as a typed first-class action — `action.KeyEvent` (`internal/action/catalog.go:532`), payload `KeyEventInput` (`proto/drift/v1/action.proto:174`) — and the device adapter already admits the argv they produce: `shell input keyevent <n>` for a canonical decimal in `[1, 10000]` (`internal/edge/adb/command.go:448`). Nothing new is admitted by wiring them, and nothing about them is free text.

They reach the device by the **same path the panel's navigation bar and the operator's own keyboard already use** (`LiveMirrorDeviceSession.sendKey`, `apps/console/src/pages/live-mirror-surface.tsx:269`), so there is one key-event path and not two: the same intent, the same lease, policy and control session, the same render-space-independent delivery. The console refuses locally only the case it can see it cannot describe — a frame that cannot name the observation a key event's catalog entry requires — and says which fact is missing rather than asking the kernel to refuse an intent it built (`apps/console/src/pages/live-mirror-surface.tsx:200-207`).

### Change Device is navigation, and it belongs in the panel

`Change Device` never was a device command: it selects which device the panel controls. It is wired as a picker of the workspace's devices that moves the frame's control session — the current device's session is ended through the kernel and the chosen device's is opened through the kernel, so the move is two dispatched intents and their answers, not a local state flip. The alternative path already exists (close the frame and click another phone) and is what the panel did before; a picker is the same operation without losing the frame's position, and it is the only way to move the frame to a second device, because clicking another phone while a frame is open selects it as a *follower*.

### Volume's read-back is NOT available, and is named here rather than claimed

ARC-138's criterion 3 asks that a postcondition be read back "where it is verifiable (rotation, autofill, volume)". Volume is **not verifiable on this plane today**: the adapter's only admitted reads are the four settings read-backs, the property allow-list, the render-size read and the dump/cat/remove family (`internal/edge/adb/command.go:350-432`), and none of them reports a media volume. Adding one means a new admitted read of the device's audio service, built to the grammar that service accepts and **confirmed against a device** — which this card cannot do on a fake-device-first bench under the real-device gate. So the wired volume commands carry the catalog's own key-event postcondition and the doc records the gap instead of dressing it up: a volume read admission is the remaining work item, not a claim made here.

`Power Button` has the same shape for a different reason: a power press changes the device's screen state, which is not a device *setting* and has no admitted read either.

## The nine controls that are not rendered, and exactly what each needs

Nine controls, grouped into eight pieces of work. Each is a control the operator had and no longer sees: that is a withdrawal from the operator's surface, so it is recorded here with the capability that is missing and the piece of work that restores it.

1. **Lock Rotate.** The owner asked for this one by name ("including the lock rotate we already have but per device"). It is the closest of the nine: the kind exists (`action.RotationLock`, `internal/action/catalog.go:590`), the device-side argv exists (the two writes and the two read-backs, `internal/edge/adb/command.go:365-368`, `:382-385`), it is dispatchable from the executor (`internal/edge/execution/input_adapter.go:488`) and its postcondition is the read-back (`internal/edge/execution/input_dispatch.go:948-961`). What is missing is a **surface that can name one device**: the only path to these kinds today is the fleet apply (`proto/drift/v1/device_settings.proto:184`), whose request deliberately names no device list (`:147`). Restoring it means widening that surface — or adding a per-device catalogued-settings intent — which is a contract change with its own safety argument, so it is the next increment and not a silent edit inside this one.
2. **Reboot.** Needs a new catalogued lifecycle kind and its own allow-list recogniser, and its postcondition is the problem: a reboot is a departure the transport watcher reports and a return a later observation confirms, so there is nothing to read synchronously and the kernel would report every successful reboot as indeterminate. That needs the asynchronous postcondition design first.
3. **Switch Keyboard.** Needs a new catalogued operation over the device's input-method service. It also needs an owner ruling: "switch keyboard" does not say *which* keyboard, and a control that changes the IME to a console-chosen one is a different operation from one that offers the installed IMEs.
4. **Install APK**, 5. **Import File**, 6. **Export File.** These three are one gap: a path story that authorizes no arbitrary path from UI input (AGENTS.md section 5). The device adapter addressable-path rule is deliberately narrow — `/sdcard/drift-<id>.xml` and nothing else (`internal/edge/adb/command.go:91`) — and the artifact store holds bytes on the host. Restoring them means an artifact-store → device push/pull path with an admitted destination set, and the owner's ruling on that set.
7. **ADB Command.** The ADVANCED form. Per AGENTS.md section 3 it is *not* free text: a curated set of admitted parameterized operations chosen from a list, plus the advanced form's own conditions (separate recogniser, exact-argv confirmation, append-only audit naming argv/actor/device/outcome, explicit control session). This card owns **proposing** the catalogue; the owner approves which operations are in it, which is why it is not implemented here.
8. **Quick Phrase.** Typed text already exists as a first-class action over an opaque reference handle, and it is reachable today — through the operator's own keyboard, which the frame owns (`AGENTS.md` section 7). "Quick Phrase" adds something else: a stored, named set of phrases. That is a new data surface, and a free-text box would duplicate the keyboard rather than add a phrase.

## Where a refusal reaches no device

Every wired command's refusal is the kernel's own, produced before dispatch, and the console states it where the operator is reading: the frame's notice line and its info control for a key event, the frame's control-refusal line for a control session that was not granted, and the capture path's own sentence for a device with no single current transport endpoint. A control whose input is not ready is disabled *and* its reason is named; it never silently does nothing.
