package media

import (
	"encoding/binary"
	"testing"
)

func controlBytes(kind MirrorControlEventKind, sequence, gesture, generation uint64, final bool) []byte {
	data := make([]byte, MirrorControlMessageSize)
	data[0], data[1] = MirrorControlVersion, byte(kind)
	if final {
		data[2] = 1
	}
	binary.BigEndian.PutUint64(data[8:16], sequence)
	binary.BigEndian.PutUint64(data[16:24], gesture)
	binary.BigEndian.PutUint64(data[32:40], generation)
	if kind == MirrorControlKey {
		binary.BigEndian.PutUint32(data[56:60], 3)
		binary.BigEndian.PutUint32(data[60:64], 1)
	} else {
		binary.BigEndian.PutUint32(data[40:44], 1080)
		binary.BigEndian.PutUint32(data[44:48], 2280)
		binary.BigEndian.PutUint32(data[48:52], 540)
		binary.BigEndian.PutUint32(data[52:56], 1140)
	}
	return data
}

func followerSelectionBytes(sequence, generation uint64, followers ...string) []byte {
	size := MirrorControlMessageSize + 2
	for _, follower := range followers {
		size += 2 + len(follower)
	}
	data := make([]byte, size)
	data[0], data[1] = MirrorControlCurrentVersion, byte(MirrorControlSetFollowers)
	binary.BigEndian.PutUint64(data[8:16], sequence)
	binary.BigEndian.PutUint64(data[32:40], generation)
	payload := data[MirrorControlMessageSize:]
	binary.BigEndian.PutUint16(payload[:2], uint16(len(followers)))
	offset := 2
	for _, follower := range followers {
		binary.BigEndian.PutUint16(payload[offset:offset+2], uint16(len(follower)))
		offset += 2
		copy(payload[offset:], follower)
		offset += len(follower)
	}
	return data
}

func TestMirrorControlMessageMapsToPhysicalSessionInput(t *testing.T) {
	for name, testCase := range map[string]struct {
		message []byte
		kind    string
	}{
		"down":   {controlBytes(MirrorControlTouchDown, 1, 7, 9, false), MirrorInputTouchDown},
		"move":   {controlBytes(MirrorControlTouchMove, 2, 7, 9, false), MirrorInputTouchMove},
		"up":     {controlBytes(MirrorControlTouchUp, 3, 7, 9, true), MirrorInputTouchUp},
		"cancel": {controlBytes(MirrorControlTouchCancel, 3, 7, 9, true), MirrorInputTouchCancel},
		"key":    {controlBytes(MirrorControlKey, 4, 0, 9, true), MirrorInputKeyEvent},
	} {
		t.Run(name, func(t *testing.T) {
			message, err := DecodeMirrorControlMessage(testCase.message)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			input, err := message.MirrorInput()
			if err != nil {
				t.Fatalf("map: %v", err)
			}
			if input.Kind != testCase.kind {
				t.Fatalf("kind = %q, want %q", input.Kind, testCase.kind)
			}
			if message.Kind == MirrorControlKey {
				if input.KeyCode != 3 || input.Repeat != 1 {
					t.Fatalf("key input = %#v", input)
				}
			} else if input.X != 540 || input.Y != 1140 || input.FrameWidth != 1080 || input.FrameHeight != 2280 {
				t.Fatalf("touch input = %#v", input)
			}
		})
	}
}

func TestMirrorControlMessageDecodesOnlyTheBoundedBinaryContract(t *testing.T) {
	message, err := DecodeMirrorControlMessage(controlBytes(MirrorControlTouchDown, 1, 7, 9, false))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if message.Kind != MirrorControlTouchDown || message.Sequence != 1 || message.GestureID != 7 || message.Generation != 9 || message.X != 540 || message.Y != 1140 {
		t.Fatalf("decoded message = %#v", message)
	}
	for name, data := range map[string][]byte{
		"short":             make([]byte, MirrorControlMessageSize-1),
		"unknown version":   append([]byte{3}, make([]byte, MirrorControlMessageSize-1)...),
		"move marked final": controlBytes(MirrorControlTouchMove, 2, 7, 9, true),
		"up not final":      controlBytes(MirrorControlTouchUp, 2, 7, 9, false),
		"key repeats multiple untracked actions": func() []byte {
			value := controlBytes(MirrorControlKey, 4, 0, 9, true)
			binary.BigEndian.PutUint32(value[60:64], 2)
			return value
		}(),
		"reserved payload": func() []byte {
			value := controlBytes(MirrorControlTouchDown, 2, 7, 9, false)
			value[64] = 1
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeMirrorControlMessage(data); err == nil {
				t.Fatal("invalid control message was accepted")
			}
		})
	}
}

func TestMirrorControlMessageCarriesBoundedFollowerSelectionAndGestureKind(t *testing.T) {
	selection, err := DecodeMirrorControlMessage(followerSelectionBytes(1, 9, "device-a", "device-b"))
	if err != nil {
		t.Fatalf("decode follower selection: %v", err)
	}
	if selection.Kind != MirrorControlSetFollowers || selection.Version != MirrorControlCurrentVersion || len(selection.FollowerDeviceIDs) != 2 || selection.FollowerDeviceIDs[0] != "device-a" || selection.FollowerDeviceIDs[1] != "device-b" {
		t.Fatalf("decoded follower selection = %#v", selection)
	}

	up := controlBytes(MirrorControlTouchUp, 2, 7, 9, true)
	up[0] = MirrorControlCurrentVersion
	up[68] = MirrorControlGestureSwipe
	binary.BigEndian.PutUint32(up[64:68], 900)
	message, err := DecodeMirrorControlMessage(up)
	if err != nil {
		t.Fatalf("decode v2 swipe release: %v", err)
	}
	if message.GestureKind != MirrorControlGestureSwipe || message.DurationMS != 900 {
		t.Fatalf("terminal gesture metadata = kind %d duration %d", message.GestureKind, message.DurationMS)
	}

	for name, data := range map[string][]byte{
		"too many followers": followerSelectionBytes(1, 9, makeFollowerIDs(MirrorControlMaxFollowers+1)...),
		"trailing follower bytes": append(followerSelectionBytes(1, 9, "device-a"), 0),
		"swipe without duration": func() []byte {
			value := controlBytes(MirrorControlTouchUp, 2, 7, 9, true)
			value[0] = MirrorControlCurrentVersion
			value[68] = MirrorControlGestureSwipe
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeMirrorControlMessage(data); err == nil {
				t.Fatal("invalid realtime follower message was accepted")
			}
		})
	}
}

func makeFollowerIDs(count int) []string {
	ids := make([]string, count)
	for index := range ids {
		ids[index] = "device-" + string(rune('a'+index))
	}
	return ids
}

func TestMirrorControlSequenceRejectsStaleAndOutOfOrderTerminalEvents(t *testing.T) {
	sequence, err := NewMirrorControlSequence(9)
	if err != nil {
		t.Fatal(err)
	}
	decode := func(data []byte) MirrorControlMessage {
		message, err := DecodeMirrorControlMessage(data)
		if err != nil {
			t.Fatal(err)
		}
		return message
	}
	if err := sequence.Accept(decode(controlBytes(MirrorControlTouchDown, 1, 7, 9, false))); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := sequence.Accept(decode(controlBytes(MirrorControlTouchMove, 2, 7, 9, false))); err != nil {
		t.Fatalf("move: %v", err)
	}
	if err := sequence.Accept(decode(controlBytes(MirrorControlTouchUp, 3, 7, 9, true))); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := sequence.Accept(decode(followerSelectionBytes(4, 9, "follower-1"))); err != nil {
		t.Fatalf("follower selection between gestures: %v", err)
	}

	for name, message := range map[string]MirrorControlMessage{
		"duplicate terminal": decode(controlBytes(MirrorControlTouchUp, 4, 7, 9, true)),
		"out of order":       decode(controlBytes(MirrorControlKey, 3, 0, 9, true)),
		"stale generation":   decode(controlBytes(MirrorControlKey, 5, 0, 8, true)),
	} {
		t.Run(name, func(t *testing.T) {
			if err := sequence.Accept(message); err == nil {
				t.Fatal("invalid sequence was accepted")
			}
		})
	}
	if err := sequence.Accept(decode(controlBytes(MirrorControlTouchDown, 6, 8, 9, false))); err != nil {
		t.Fatalf("next gesture: %v", err)
	}
}
