package transportconnect_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/action"
	transportconnect "drift.local/drift-next/internal/transport/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// The render frame every coordinate-bearing case below is expressed in. It is
// the `wm size` OVERRIDE size, never a physical panel size.
const (
	testRenderWidth  = 1080
	testRenderHeight = 2280
	testObservation  = "observation-1"
)

func testRenderSpace() *driftv1.DeviceRenderSpace {
	return &driftv1.DeviceRenderSpace{
		RenderWidth:      testRenderWidth,
		RenderHeight:     testRenderHeight,
		ObservationToken: testObservation,
	}
}

func typedInputIntent(kind driftv1.ActionKind) *driftv1.ActionIntent {
	return &driftv1.ActionIntent{
		Workspace:        &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
		DeviceId:         "device-1",
		Kind:             kind,
		ObservationToken: testObservation,
	}
}

func roundTripActionIntent(t *testing.T, intent *driftv1.ActionIntent) *driftv1.ActionIntent {
	t.Helper()
	encoded, err := proto.Marshal(intent)
	if err != nil {
		t.Fatalf("marshal action intent: %v", err)
	}
	decoded := &driftv1.ActionIntent{}
	if err := proto.Unmarshal(encoded, decoded); err != nil {
		t.Fatalf("unmarshal action intent: %v", err)
	}
	return decoded
}

// Each of the five device inputs is a first-class typed action: it round-trips
// through the generated types with its payload — and only its payload — intact.
func TestTypedDeviceInputContractRoundTripsEveryFirstClassAction(t *testing.T) {
	cases := []struct {
		name  string
		kind  driftv1.ActionKind
		apply func(intent *driftv1.ActionIntent)
		check func(t *testing.T, intent *driftv1.ActionIntent)
	}{
		{
			name: "tap",
			kind: driftv1.ActionKind_ACTION_KIND_TAP,
			apply: func(intent *driftv1.ActionIntent) {
				intent.DeviceInput = &driftv1.ActionIntent_Tap{Tap: &driftv1.TapInput{
					Target: &driftv1.SemanticTarget{ResourceId: "save-button", AccessibilityLabel: "Save"},
				}}
			},
			check: func(t *testing.T, intent *driftv1.ActionIntent) {
				tap := intent.GetTap()
				if tap == nil || tap.GetTarget().GetResourceId() != "save-button" || tap.GetTarget().GetAccessibilityLabel() != "Save" {
					t.Fatalf("tap payload after round trip = %#v", tap)
				}
			},
		},
		{
			name: "swipe",
			kind: driftv1.ActionKind_ACTION_KIND_SWIPE,
			apply: func(intent *driftv1.ActionIntent) {
				intent.DeviceInput = &driftv1.ActionIntent_Swipe{Swipe: &driftv1.SwipeInput{
					Start:       &driftv1.DevicePoint{X: 540, Y: 1800},
					End:         &driftv1.DevicePoint{X: 540, Y: 400},
					DurationMs:  250,
					RenderSpace: testRenderSpace(),
				}}
			},
			check: func(t *testing.T, intent *driftv1.ActionIntent) {
				swipe := intent.GetSwipe()
				if swipe == nil || swipe.GetStart().GetX() != 540 || swipe.GetStart().GetY() != 1800 || swipe.GetEnd().GetY() != 400 || swipe.GetDurationMs() != 250 {
					t.Fatalf("swipe payload after round trip = %#v", swipe)
				}
			},
		},
		{
			name: "type text",
			kind: driftv1.ActionKind_ACTION_KIND_TEXT_INPUT,
			apply: func(intent *driftv1.ActionIntent) {
				intent.DeviceInput = &driftv1.ActionIntent_TypeText{TypeText: &driftv1.TypeTextInput{
					Text: &driftv1.SensitiveTextReference{Handle: "value-ref-1", ValueLength: 24},
				}}
			},
			check: func(t *testing.T, intent *driftv1.ActionIntent) {
				reference := intent.GetTypeText().GetText()
				if reference == nil || reference.GetHandle() != "value-ref-1" || reference.GetValueLength() != 24 {
					t.Fatalf("typed text reference after round trip = %#v", reference)
				}
			},
		},
		{
			name: "key event",
			kind: driftv1.ActionKind_ACTION_KIND_KEY_EVENT,
			apply: func(intent *driftv1.ActionIntent) {
				intent.DeviceInput = &driftv1.ActionIntent_KeyEvent{KeyEvent: &driftv1.KeyEventInput{KeyCode: 4}}
			},
			check: func(t *testing.T, intent *driftv1.ActionIntent) {
				if intent.GetKeyEvent().GetKeyCode() != 4 {
					t.Fatalf("key event payload after round trip = %#v", intent.GetKeyEvent())
				}
			},
		},
		{
			name: "launch app",
			kind: driftv1.ActionKind_ACTION_KIND_LAUNCH_APP,
			apply: func(intent *driftv1.ActionIntent) {
				intent.DeviceInput = &driftv1.ActionIntent_LaunchApp{LaunchApp: &driftv1.LaunchAppInput{
					PackageName:  "com.example.app",
					ActivityName: "com.example.app.MainActivity",
				}}
			},
			check: func(t *testing.T, intent *driftv1.ActionIntent) {
				launch := intent.GetLaunchApp()
				if launch == nil || launch.GetPackageName() != "com.example.app" || launch.GetActivityName() != "com.example.app.MainActivity" {
					t.Fatalf("launch payload after round trip = %#v", launch)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			intent := typedInputIntent(tc.kind)
			tc.apply(intent)
			if err := transportconnect.ValidateDeviceInputIntent(intent); err != nil {
				t.Fatalf("typed %s input was refused: %v", tc.name, err)
			}
			decoded := roundTripActionIntent(t, intent)
			if decoded.GetKind() != tc.kind {
				t.Fatalf("kind after round trip = %v, want %v", decoded.GetKind(), tc.kind)
			}
			tc.check(t, decoded)
			if err := transportconnect.ValidateDeviceInputIntent(decoded); err != nil {
				t.Fatalf("round-tripped %s input was refused: %v", tc.name, err)
			}
		})
	}
}

// A coordinate-bearing input carries the render space it was measured in, and a
// coordinate that arrives without one — or outside the one it names — is
// refused instead of scaled.
func TestCoordinateBearingDeviceInputCarriesItsDeviceRenderSpace(t *testing.T) {
	valid := typedInputIntent(driftv1.ActionKind_ACTION_KIND_SWIPE)
	valid.DeviceInput = &driftv1.ActionIntent_Swipe{Swipe: &driftv1.SwipeInput{
		Start:       &driftv1.DevicePoint{X: 540, Y: 1800},
		End:         &driftv1.DevicePoint{X: 540, Y: 400},
		DurationMs:  250,
		RenderSpace: testRenderSpace(),
	}}
	if err := transportconnect.ValidateDeviceInputIntent(valid); err != nil {
		t.Fatalf("swipe carrying its render space was refused: %v", err)
	}
	decoded := roundTripActionIntent(t, valid)
	space := decoded.GetSwipe().GetRenderSpace()
	if space.GetRenderWidth() != testRenderWidth || space.GetRenderHeight() != testRenderHeight || space.GetObservationToken() != testObservation {
		t.Fatalf("render space after round trip = %#v", space)
	}

	refused := []struct {
		name   string
		intent func() *driftv1.ActionIntent
	}{
		{
			name: "swipe without a render space",
			intent: func() *driftv1.ActionIntent {
				intent := typedInputIntent(driftv1.ActionKind_ACTION_KIND_SWIPE)
				intent.DeviceInput = &driftv1.ActionIntent_Swipe{Swipe: &driftv1.SwipeInput{
					Start:      &driftv1.DevicePoint{X: 540, Y: 1800},
					End:        &driftv1.DevicePoint{X: 540, Y: 400},
					DurationMs: 250,
				}}
				return intent
			},
		},
		{
			name: "swipe whose render space names another observation",
			intent: func() *driftv1.ActionIntent {
				intent := typedInputIntent(driftv1.ActionKind_ACTION_KIND_SWIPE)
				intent.DeviceInput = &driftv1.ActionIntent_Swipe{Swipe: &driftv1.SwipeInput{
					Start:      &driftv1.DevicePoint{X: 540, Y: 1800},
					End:        &driftv1.DevicePoint{X: 540, Y: 400},
					DurationMs: 250,
					RenderSpace: &driftv1.DeviceRenderSpace{
						RenderWidth: testRenderWidth, RenderHeight: testRenderHeight, ObservationToken: "observation-2",
					},
				}}
				return intent
			},
		},
		{
			name: "swipe outside its declared render space",
			intent: func() *driftv1.ActionIntent {
				intent := typedInputIntent(driftv1.ActionKind_ACTION_KIND_SWIPE)
				intent.DeviceInput = &driftv1.ActionIntent_Swipe{Swipe: &driftv1.SwipeInput{
					Start:       &driftv1.DevicePoint{X: 1440, Y: 1800},
					End:         &driftv1.DevicePoint{X: 540, Y: 400},
					DurationMs:  250,
					RenderSpace: testRenderSpace(),
				}}
				return intent
			},
		},
		{
			name: "tap point without a render space",
			intent: func() *driftv1.ActionIntent {
				intent := typedInputIntent(driftv1.ActionKind_ACTION_KIND_TAP)
				intent.DeviceInput = &driftv1.ActionIntent_Tap{Tap: &driftv1.TapInput{
					Point: &driftv1.DevicePoint{X: 540, Y: 1800},
				}}
				return intent
			},
		},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			err := transportconnect.ValidateDeviceInputIntent(tc.intent())
			if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
				t.Fatalf("%s: code = %v err = %v, want invalid_argument", tc.name, connectrpc.CodeOf(err), err)
			}
		})
	}
}

// Text content never travels in this contract. The typed text payload has no
// plaintext field at all, so no log line, error, or persisted record can
// render it; the value is named by an opaque reference and resolved outside it.
func TestTypedTextPayloadHasNoPlaintextField(t *testing.T) {
	for _, name := range []protoreflect.Name{"TypeTextInput", "SensitiveTextReference"} {
		message := driftv1.File_drift_v1_action_proto.Messages().ByName(name)
		if message == nil {
			t.Fatalf("message %s is missing from the action contract", name)
		}
		fields := message.Fields()
		for index := 0; index < fields.Len(); index++ {
			field := fields.Get(index)
			if field.Name() == "handle" {
				continue
			}
			if field.Kind() == protoreflect.StringKind || field.Kind() == protoreflect.BytesKind {
				t.Fatalf("%s.%s can carry text content; typed text travels by reference only", name, field.Name())
			}
		}
	}

	intent := typedInputIntent(driftv1.ActionKind_ACTION_KIND_TEXT_INPUT)
	intent.DeviceInput = &driftv1.ActionIntent_TypeText{TypeText: &driftv1.TypeTextInput{
		Text: &driftv1.SensitiveTextReference{Handle: "value-ref-1", ValueLength: 24},
	}}
	if err := transportconnect.ValidateDeviceInputIntent(intent); err != nil {
		t.Fatalf("typed text reference was refused: %v", err)
	}
	if rendered := fmt.Sprintf("%v", intent); !strings.Contains(rendered, "value-ref-1") {
		t.Fatalf("rendered intent does not carry the reference: %s", rendered)
	}
}

// The only string a caller can put a text value into is the reference handle,
// and the contract refuses a handle that is not an opaque reference — without
// echoing what was submitted.
func TestTypedTextRejectsATextShapedHandleAndNeverRendersIt(t *testing.T) {
	const plaintext = "correct horse battery staple"
	intent := typedInputIntent(driftv1.ActionKind_ACTION_KIND_TEXT_INPUT)
	intent.DeviceInput = &driftv1.ActionIntent_TypeText{TypeText: &driftv1.TypeTextInput{
		Text: &driftv1.SensitiveTextReference{Handle: plaintext, ValueLength: uint32(len(plaintext))},
	}}
	err := transportconnect.ValidateDeviceInputIntent(intent)
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("text-shaped handle: code = %v err = %v, want invalid_argument", connectrpc.CodeOf(err), err)
	}
	if err != nil && strings.Contains(err.Error(), plaintext) {
		t.Fatalf("the rejection rendered the rejected value: %v", err)
	}
}

// The contract refuses an action whose required typed payload is absent or
// incomplete, and leaves the non-input kinds untouched.
func TestDeviceInputContractRejectsAnActionMissingItsRequiredPayload(t *testing.T) {
	refused := []struct {
		name   string
		intent *driftv1.ActionIntent
	}{
		{
			name:   "tap with no payload at all",
			intent: typedInputIntent(driftv1.ActionKind_ACTION_KIND_TAP),
		},
		{
			name: "tap with neither a semantic target nor a point",
			intent: func() *driftv1.ActionIntent {
				intent := typedInputIntent(driftv1.ActionKind_ACTION_KIND_TAP)
				intent.DeviceInput = &driftv1.ActionIntent_Tap{Tap: &driftv1.TapInput{}}
				return intent
			}(),
		},
		{
			name: "tap that names both a semantic target and a point",
			intent: func() *driftv1.ActionIntent {
				intent := typedInputIntent(driftv1.ActionKind_ACTION_KIND_TAP)
				intent.DeviceInput = &driftv1.ActionIntent_Tap{Tap: &driftv1.TapInput{
					Target:      &driftv1.SemanticTarget{ResourceId: "save-button"},
					Point:       &driftv1.DevicePoint{X: 540, Y: 1800},
					RenderSpace: testRenderSpace(),
				}}
				return intent
			}(),
		},
		{
			name:   "swipe with no payload at all",
			intent: typedInputIntent(driftv1.ActionKind_ACTION_KIND_SWIPE),
		},
		{
			name: "swipe with no endpoints",
			intent: func() *driftv1.ActionIntent {
				intent := typedInputIntent(driftv1.ActionKind_ACTION_KIND_SWIPE)
				intent.DeviceInput = &driftv1.ActionIntent_Swipe{Swipe: &driftv1.SwipeInput{RenderSpace: testRenderSpace()}}
				return intent
			}(),
		},
		{
			name: "swipe with an unbounded duration",
			intent: func() *driftv1.ActionIntent {
				intent := typedInputIntent(driftv1.ActionKind_ACTION_KIND_SWIPE)
				intent.DeviceInput = &driftv1.ActionIntent_Swipe{Swipe: &driftv1.SwipeInput{
					Start:       &driftv1.DevicePoint{X: 540, Y: 1800},
					End:         &driftv1.DevicePoint{X: 540, Y: 400},
					DurationMs:  600001,
					RenderSpace: testRenderSpace(),
				}}
				return intent
			}(),
		},
		{
			name: "typed text with no reference",
			intent: func() *driftv1.ActionIntent {
				intent := typedInputIntent(driftv1.ActionKind_ACTION_KIND_TEXT_INPUT)
				intent.DeviceInput = &driftv1.ActionIntent_TypeText{TypeText: &driftv1.TypeTextInput{}}
				return intent
			}(),
		},
		{
			name: "typed text with no declared length",
			intent: func() *driftv1.ActionIntent {
				intent := typedInputIntent(driftv1.ActionKind_ACTION_KIND_TEXT_INPUT)
				intent.DeviceInput = &driftv1.ActionIntent_TypeText{TypeText: &driftv1.TypeTextInput{
					Text: &driftv1.SensitiveTextReference{Handle: "value-ref-1"},
				}}
				return intent
			}(),
		},
		{
			name: "key event with no key code",
			intent: func() *driftv1.ActionIntent {
				intent := typedInputIntent(driftv1.ActionKind_ACTION_KIND_KEY_EVENT)
				intent.DeviceInput = &driftv1.ActionIntent_KeyEvent{KeyEvent: &driftv1.KeyEventInput{}}
				return intent
			}(),
		},
		{
			name: "launch app with no package",
			intent: func() *driftv1.ActionIntent {
				intent := typedInputIntent(driftv1.ActionKind_ACTION_KIND_LAUNCH_APP)
				intent.DeviceInput = &driftv1.ActionIntent_LaunchApp{LaunchApp: &driftv1.LaunchAppInput{}}
				return intent
			}(),
		},
		{
			name: "launch app with a package name that is not a package",
			intent: func() *driftv1.ActionIntent {
				intent := typedInputIntent(driftv1.ActionKind_ACTION_KIND_LAUNCH_APP)
				intent.DeviceInput = &driftv1.ActionIntent_LaunchApp{LaunchApp: &driftv1.LaunchAppInput{PackageName: "not a package"}}
				return intent
			}(),
		},
		{
			name: "tap carrying a swipe payload",
			intent: func() *driftv1.ActionIntent {
				intent := typedInputIntent(driftv1.ActionKind_ACTION_KIND_TAP)
				intent.DeviceInput = &driftv1.ActionIntent_Swipe{Swipe: &driftv1.SwipeInput{
					Start:       &driftv1.DevicePoint{X: 540, Y: 1800},
					End:         &driftv1.DevicePoint{X: 540, Y: 400},
					DurationMs:  250,
					RenderSpace: testRenderSpace(),
				}}
				return intent
			}(),
		},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			err := transportconnect.ValidateDeviceInputIntent(tc.intent)
			if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
				t.Fatalf("%s: code = %v err = %v, want invalid_argument", tc.name, connectrpc.CodeOf(err), err)
			}
		})
	}

	observe := typedInputIntent(driftv1.ActionKind_ACTION_KIND_OBSERVE)
	if err := transportconnect.ValidateDeviceInputIntent(observe); err != nil {
		t.Fatalf("non-input kind observe was affected by the typed input contract: %v", err)
	}
	if err := transportconnect.ValidateDeviceInputIntent(nil); connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("nil intent: code = %v err = %v, want invalid_argument", connectrpc.CodeOf(err), err)
	}
}

// The refusal happens on the live submit path, before the kernel is asked to
// authorize anything.
func TestActionHandlerRefusesATapWithoutItsTypedPayload(t *testing.T) {
	db := openProductDB(t)
	handler := transportconnect.NewActionHandler(db)
	_, err := handler.SubmitAction(context.Background(), connectrpc.NewRequest(&driftv1.SubmitActionRequest{
		Context: requestContext("action-typed-tap-missing-payload"),
		Intent: &driftv1.ActionIntent{
			Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-a"},
			DeviceId:  "device-1",
			Kind:      driftv1.ActionKind_ACTION_KIND_TAP,
		},
	}))
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("untyped tap submit: code = %v err = %v, want invalid_argument", connectrpc.CodeOf(err), err)
	}
}

// There is no generic member a caller could use to run arbitrary commands on a
// device: no shell, exec, command, argv, raw or free-form payload field exists
// anywhere in the action contract, and the typed input oneof names exactly the
// five first-class inputs.
func TestActionContractDeclaresNoGenericCommandOrShellMember(t *testing.T) {
	forbidden := map[protoreflect.Name]bool{
		"shell": true, "exec": true, "command": true, "commands": true,
		"argv": true, "raw": true, "raw_command": true, "script": true, "payload": true,
	}
	messages := driftv1.File_drift_v1_action_proto.Messages()
	for index := 0; index < messages.Len(); index++ {
		message := messages.Get(index)
		fields := message.Fields()
		for fieldIndex := 0; fieldIndex < fields.Len(); fieldIndex++ {
			if forbidden[fields.Get(fieldIndex).Name()] {
				t.Fatalf("%s.%s is a generic execution escape hatch", message.Name(), fields.Get(fieldIndex).Name())
			}
		}
	}

	intent := driftv1.File_drift_v1_action_proto.Messages().ByName("ActionIntent")
	if intent == nil {
		t.Fatal("ActionIntent is missing from the action contract")
	}
	oneofs := intent.Oneofs()
	if oneofs.Len() != 1 || oneofs.Get(0).Name() != "device_input" {
		t.Fatalf("ActionIntent oneofs = %v, want exactly device_input", oneofs)
	}
	want := []protoreflect.Name{"tap", "swipe", "type_text", "key_event", "launch_app"}
	fields := oneofs.Get(0).Fields()
	if fields.Len() != len(want) {
		t.Fatalf("typed device input members = %d, want %d", fields.Len(), len(want))
	}
	for index, name := range want {
		if got := fields.Get(index).Name(); got != name {
			t.Fatalf("typed device input member %d = %q, want %q", index, got, name)
		}
		if got := fields.Get(index).Number(); got != protoreflect.FieldNumber(15+index) {
			t.Fatalf("typed device input member %q number = %d, want %d", name, got, 15+index)
		}
	}

	// The launch action is a first-class catalog kind, not an unclassified
	// string: it carries the same safety metadata as every other input.
	spec, ok := action.Lookup(action.LaunchApp)
	if !ok {
		t.Fatal("launch_app is not a declared catalog kind")
	}
	if !spec.Mutating || !spec.RequiresObservation || spec.Risk == "" || spec.Retry == "" {
		t.Fatalf("launch_app specification is incomplete: %#v", spec)
	}
}
