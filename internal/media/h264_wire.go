package media

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// H264FramePacketHeaderBytes is the fixed wire header for one Annex-B access unit.
	H264FramePacketHeaderBytes = 36
	// MaxH264FramePayloadBytes bounds the amount of data retained or accepted per frame.
	MaxH264FramePayloadBytes = 2 << 20
	maxH264FrameDimension    = 16_384
)

var h264FramePacketMagic = [4]byte{'D', 'R', 'H', '1'}

// H264FramePacket is one bounded Annex-B H.264 access unit plus its active render
// dimensions. Packets are intentionally self-contained so the WebCodecs path does
// not need to buffer or reinterpret fragmented MP4.
type H264FramePacket struct {
	Sequence    uint64
	TimestampUS uint64
	Key         bool
	Width       uint32
	Height      uint32
	Data        []byte
}

// EncodeH264FramePacket serializes a single bounded frame for the authenticated
// live-mirror WebSocket. Callers should send each returned packet immediately and
// must not build an unbounded queue of encoded frames.
func EncodeH264FramePacket(packet H264FramePacket) ([]byte, error) {
	if err := validateH264FramePacket(packet); err != nil {
		return nil, err
	}
	wire := make([]byte, H264FramePacketHeaderBytes+len(packet.Data))
	copy(wire[:4], h264FramePacketMagic[:])
	wire[4] = 1 // protocol version
	if packet.Key {
		wire[5] = 1
	}
	binary.BigEndian.PutUint64(wire[8:16], packet.Sequence)
	binary.BigEndian.PutUint64(wire[16:24], packet.TimestampUS)
	binary.BigEndian.PutUint32(wire[24:28], packet.Width)
	binary.BigEndian.PutUint32(wire[28:32], packet.Height)
	binary.BigEndian.PutUint32(wire[32:36], uint32(len(packet.Data)))
	copy(wire[H264FramePacketHeaderBytes:], packet.Data)
	return wire, nil
}

// DecodeH264FramePacket validates and decodes exactly one wire packet. Trailing
// bytes are rejected so a caller cannot accidentally accept concatenated frames.
func DecodeH264FramePacket(wire []byte) (H264FramePacket, error) {
	if len(wire) < H264FramePacketHeaderBytes {
		return H264FramePacket{}, errors.New("H.264 frame packet header is truncated")
	}
	if string(wire[:4]) != string(h264FramePacketMagic[:]) {
		return H264FramePacket{}, errors.New("H.264 frame packet magic is invalid")
	}
	if wire[4] != 1 {
		return H264FramePacket{}, fmt.Errorf("unsupported H.264 frame packet version %d", wire[4])
	}
	if wire[5] > 1 || wire[6] != 0 || wire[7] != 0 {
		return H264FramePacket{}, errors.New("H.264 frame packet flags or reserved bytes are invalid")
	}
	payloadBytes := binary.BigEndian.Uint32(wire[32:36])
	if payloadBytes == 0 || payloadBytes > MaxH264FramePayloadBytes {
		return H264FramePacket{}, fmt.Errorf("H.264 frame payload size %d is outside the supported bound", payloadBytes)
	}
	if uint64(len(wire)) != uint64(H264FramePacketHeaderBytes)+uint64(payloadBytes) {
		return H264FramePacket{}, errors.New("H.264 frame packet payload length does not match its header")
	}
	packet := H264FramePacket{
		Sequence:    binary.BigEndian.Uint64(wire[8:16]),
		TimestampUS: binary.BigEndian.Uint64(wire[16:24]),
		Key:         wire[5] == 1,
		Width:       binary.BigEndian.Uint32(wire[24:28]),
		Height:      binary.BigEndian.Uint32(wire[28:32]),
		Data:        wire[H264FramePacketHeaderBytes:],
	}
	if err := validateH264FramePacket(packet); err != nil {
		return H264FramePacket{}, err
	}
	return packet, nil
}

func validateH264FramePacket(packet H264FramePacket) error {
	if packet.Sequence == 0 {
		return errors.New("H.264 frame sequence must be positive")
	}
	if packet.Width == 0 || packet.Height == 0 || packet.Width > maxH264FrameDimension || packet.Height > maxH264FrameDimension {
		return fmt.Errorf("H.264 frame dimensions %dx%d are outside the supported bound", packet.Width, packet.Height)
	}
	if len(packet.Data) == 0 || len(packet.Data) > MaxH264FramePayloadBytes {
		return fmt.Errorf("H.264 frame payload size %d is outside the supported bound", len(packet.Data))
	}
	if !hasAnnexBStartCode(packet.Data) {
		return errors.New("H.264 frame payload is not Annex-B encoded")
	}
	return nil
}

func hasAnnexBStartCode(data []byte) bool {
	for i := 0; i+3 <= len(data); i++ {
		if data[i] == 0 && data[i+1] == 0 && (data[i+2] == 1 || (i+4 <= len(data) && data[i+2] == 0 && data[i+3] == 1)) {
			return true
		}
	}
	return false
}
