// Live-session input delivery.
//
// The two ways a typed device input can reach a device are not interchangeable
// in cost, and the difference is one an operator sees. An `adb shell input` call
// spawns a process on the device for every single action - measured on this
// fleet at 100-300 ms - while the same action encoded onto a live scrcpy
// session's control socket is a write to an already-open socket, measured at
// 5-15 ms. So when an operator is working a device's live mirror, the input has
// to travel the session they are looking at: a tap that arrives 300 ms after the
// screen it was aimed at is not the same product as one that arrives in 12.
//
// This file is the seam for that, and it is deliberately narrow. It is a
// DELIVERY, never an authority: the kernel authorizes and dispatches the attempt
// first (exactly as it does for the allow-listed argv path), and this port is
// reached only afterwards, carrying the typed payload the kernel just authorized.
// scrcpy having a control channel of its own does not make the policy kernel
// decorative, so nothing here can be reached by a request that the kernel
// refused.
//
// A device with no live session is not mirrored, and its input travels the argv
// path as it always did. That is the whole routing rule: the session is the
// delivery when there is one, and the shape of the action does not change with
// the route.
package execution

import (
	"context"

	"drift.local/drift-next/internal/action"
)

// MirrorDelivery carries one authorized typed input to a device's live mirror
// session, and reports whether that device has one.
//
// A nil implementation means this process has no mirror, and every input then
// travels the argv path: the absence of a mirror is not an error, it is a
// deployment that has not armed one.
type MirrorDelivery interface {
	// Mirrored reports whether this device has a live session that could carry
	// an input right now. It is a routing decision and confers no authority: a
	// session that exists is still not permission to send anything through it.
	Mirrored(deviceID string) bool
	// DeliverInput carries one authorized coordinate-bearing or key input to
	// that session.
	DeliverInput(ctx context.Context, input MirrorDeliveryInput) error
	// DeliverText carries one released typed-text value to that session.
	//
	// The value is an argument rather than a field of a struct so it cannot
	// outlive the call: the session encodes it onto the control socket and keeps
	// no copy, and an error this call returns never quotes it. A caller that
	// could not release the value never calls this at all.
	DeliverText(ctx context.Context, deviceID string, frame RenderSpace, value string) error
}

// MirrorDeliveryInput is one authorized typed input as a live session receives
// it. It carries no text: a typed-text value has its own call so it cannot be
// held in a value that outlives the dispatch.
type MirrorDeliveryInput struct {
	// DeviceID names the device whose live session carries this input.
	DeviceID string
	// Kind is the action kind the kernel authorized.
	Kind action.Kind
	// Point and End are the gesture's coordinates in Frame, for a kind that
	// carries them.
	Point Point
	End   Point
	// DurationMS is the swipe's duration.
	DurationMS uint32
	// KeyCode and Repeat are the key event.
	KeyCode uint32
	Repeat  uint32
	// Frame is the render space the coordinate was measured in - the size the
	// device presents at. The session independently refuses a coordinate whose
	// frame is not the frame the stream is encoded at, so this travels with the
	// coordinate rather than being assumed to match.
	Frame RenderSpace
}
