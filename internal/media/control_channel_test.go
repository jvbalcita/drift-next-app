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
		"unknown version":   append([]byte{2}, make([]byte, MirrorControlMessageSize-1)...),
		"move marked final": controlBytes(MirrorControlTouchMove, 2, 7, 9, true),
		"up not final":      controlBytes(MirrorControlTouchUp, 2, 7, 9, false),
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
