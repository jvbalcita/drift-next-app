package media_test

import (
	"bytes"
	"testing"

	"drift.local/drift-next/internal/media"
)

func TestH264FramePacketRoundTripsBoundedAnnexBPicture(t *testing.T) {
	packet := media.H264FramePacket{
		Sequence:    7,
		TimestampUS: 233_000,
		Key:         true,
		Width:       1080,
		Height:      2280,
		Data:        []byte{0, 0, 0, 1, 0x67, 0x42, 0xe0, 0x1f, 0, 0, 1, 0x68, 0xce, 0x06, 0xe2, 0, 0, 1, 0x65, 0x88, 0x84},
	}

	encoded, err := media.EncodeH264FramePacket(packet)
	if err != nil {
		t.Fatalf("encode frame packet: %v", err)
	}
	decoded, err := media.DecodeH264FramePacket(encoded)
	if err != nil {
		t.Fatalf("decode frame packet: %v", err)
	}
	if decoded.Sequence != packet.Sequence || decoded.TimestampUS != packet.TimestampUS || decoded.Key != packet.Key || decoded.Width != packet.Width || decoded.Height != packet.Height || !bytes.Equal(decoded.Data, packet.Data) {
		t.Fatalf("decoded packet = %#v, want the original H.264 access unit", decoded)
	}
}

func TestH264FramePacketRejectsInvalidOrUnboundedInput(t *testing.T) {
	valid := media.H264FramePacket{
		Sequence: 1,
		Key:      true,
		Width:    720,
		Height:   1280,
		Data:     []byte{0, 0, 1, 0x65, 0x88},
	}
	cases := []struct {
		name   string
		packet media.H264FramePacket
	}{
		{name: "missing sequence", packet: media.H264FramePacket{Key: true, Width: 720, Height: 1280, Data: valid.Data}},
		{name: "missing dimension", packet: media.H264FramePacket{Sequence: 1, Key: true, Width: 0, Height: 1280, Data: valid.Data}},
		{name: "oversized frame", packet: media.H264FramePacket{Sequence: 1, Key: true, Width: 720, Height: 1280, Data: make([]byte, media.MaxH264FramePayloadBytes+1)}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := media.EncodeH264FramePacket(test.packet); err == nil {
				t.Fatal("invalid frame packet was encoded")
			}
		})
	}

	encoded, err := media.EncodeH264FramePacket(valid)
	if err != nil {
		t.Fatalf("encode valid frame packet: %v", err)
	}
	encoded[len(encoded)-1] ^= 0xff
	// A payload mutation is legal bytes on the wire; a truncated payload is not.
	if _, err := media.DecodeH264FramePacket(encoded[:len(encoded)-1]); err == nil {
		t.Fatal("truncated frame packet was decoded")
	}
}
