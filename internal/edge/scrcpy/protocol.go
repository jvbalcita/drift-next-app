// Package scrcpy speaks the part of the scrcpy 4.1 client protocol a live
// mirror needs: it pushes and starts the server on the device, owns the video
// and control sockets, and carries typed input to the device's own input
// injector.
//
// It exists so the mirror moves a raw H.264 stream off scrcpy's video socket
// and injects input through scrcpy's control socket, rather than spawning
// `adb shell input` per action: that spawns a process on the device and costs
// 100-300 ms per input, which is the difference between a device that feels
// present and one that does not.
//
// The framing below is not inferred from prose. It was read off scrcpy 4.1's
// own server (`device/Streamer.java` for the framing, `control/ControlMessage.java`
// and the client's `app/src/control_msg.c` for the control layout) and then
// confirmed against a lab SM-G9750:
//
//	stream  = codec id (u32) | session packet (u32 flags, u32 width, u32 height)
//	packet  = flags|pts (u64) | size (u32) | size bytes
//
// The session packet's flags are a u32 whose top bit marks a session packet —
// unlike a frame packet's u64, where the same bit means the same thing but the
// word is twice as wide. That asymmetry is the one place this protocol is easy
// to get wrong, so both are parsed by their own function here.
package scrcpy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	// PacketHeaderSize is the 12-byte header every frame packet carries when
	// frame metadata is requested: a big-endian u64 of flags and PTS, then a
	// big-endian u32 size.
	PacketHeaderSize = 12

	// SessionPacketSize is the 12-byte session packet: a big-endian u32 of
	// flags, then the encoded width and height as big-endian u32s.
	SessionPacketSize = 12

	// CodecHeaderSize is the codec identifier that precedes the session packet
	// when stream metadata is requested.
	CodecHeaderSize = 4

	// maxPacketBytes bounds one packet. An H.264 access unit from a phone screen
	// is orders of magnitude smaller; a size beyond this is a desynchronised
	// stream being read as a length, and must be refused rather than allocated.
	maxPacketBytes = 64 << 20

	// DisabledStreamCodec reports that the device refused to capture the stream
	// it was asked for and will send nothing further.
	DisabledStreamCodec uint32 = 0

	// ErrorStreamCodec reports that the device hit a configuration error and the
	// mirror cannot continue.
	ErrorStreamCodec uint32 = 1

	codecH264 uint32 = 0x68323634 // "h264"
)

// Packet flag bits, as the server assigns them (device/Streamer.java):
// bit 63 session, bit 62 config, bit 61 key frame, the low 61 bits a PTS in
// microseconds.
const (
	packetFlagSession  uint64 = 1 << 63
	packetFlagConfig   uint64 = 1 << 62
	packetFlagKeyFrame uint64 = 1 << 61
	packetPTSMask      uint64 = (1 << 61) - 1
)

// StreamMeta is the head of the video socket: the encoded frame size the device
// is streaming.
//
// Width and Height are not decoration. They are the coordinate frame injected
// input must be measured in, and they are the same frame the action-safety
// kernel cross-checks a declared render space against — so a size read here that
// disagrees with the device's render size is a defect to refuse, never a size to
// scale coordinates into.
type StreamMeta struct {
	Codec  uint32
	Width  int
	Height int
}

// Size reports the encoded frame size.
func (m StreamMeta) Size() (int, int) { return m.Width, m.Height }

// AccessUnit is one packet as the device produced it: either a codec
// configuration packet (SPS/PPS) or one encoded picture.
type AccessUnit struct {
	// Config reports a codec configuration packet. It carries no picture and
	// must never be forwarded as one: a receiver fed a config packet as a frame
	// decodes nothing and reports no error.
	Config bool
	// Key reports a key frame (an IDR). A receiver that has not seen one cannot
	// decode anything that follows.
	Key bool
	// PTSUS is the encoder's own presentation timestamp in microseconds. It is
	// the device's cadence, which is what RTP timestamps must follow; assuming a
	// frame rate instead is how a mirror drifts.
	PTSUS uint64
	// Data is the packet payload, in Annex-B form: NAL units separated by start
	// codes.
	Data []byte
}

// ParseStreamMeta reads the codec identifier and the session packet that open
// the video socket.
//
// A codec identifier of 0 or 1 is the device's own "I will not stream" signal
// (it refused the capture, or hit a configuration error), and is reported as its
// own error rather than parsed as a size.
func ParseStreamMeta(head []byte) (StreamMeta, error) {
	if len(head) < CodecHeaderSize+SessionPacketSize {
		return StreamMeta{}, fmt.Errorf("scrcpy: stream head is %d bytes, want %d", len(head), CodecHeaderSize+SessionPacketSize)
	}
	codec := binary.BigEndian.Uint32(head[0:4])
	switch codec {
	case DisabledStreamCodec:
		return StreamMeta{}, ErrStreamDisabled
	case ErrorStreamCodec:
		return StreamMeta{}, ErrStreamConfiguration
	case codecH264:
	default:
		return StreamMeta{}, fmt.Errorf("scrcpy: device streams codec %#08x, not h264", codec)
	}
	flags := binary.BigEndian.Uint32(head[4:8])
	if flags&0x80000000 == 0 {
		return StreamMeta{}, fmt.Errorf("scrcpy: expected a session packet, got flags %#08x", flags)
	}
	width := int(binary.BigEndian.Uint32(head[8:12]))
	height := int(binary.BigEndian.Uint32(head[12:16]))
	if width <= 0 || height <= 0 || width > maxFrameDimension || height > maxFrameDimension {
		return StreamMeta{}, fmt.Errorf("scrcpy: device reported a %dx%d stream", width, height)
	}
	return StreamMeta{Codec: codec, Width: width, Height: height}, nil
}

// maxFrameDimension bounds an encoded frame size. It is the same bound the
// device-input contract uses for a render space, so a size read here can never
// exceed a frame a coordinate is allowed to be measured in.
const maxFrameDimension = 10000

// parseFrameHeader decodes one 12-byte frame header.
func parseFrameHeader(header []byte) (config, key bool, ptsUS uint64, size int, err error) {
	if len(header) < PacketHeaderSize {
		return false, false, 0, 0, fmt.Errorf("scrcpy: frame header is %d bytes, want %d", len(header), PacketHeaderSize)
	}
	flags := binary.BigEndian.Uint64(header[0:8])
	if flags&packetFlagSession != 0 {
		return false, false, 0, 0, errors.New("scrcpy: expected a frame packet, got a session packet")
	}
	size32 := binary.BigEndian.Uint32(header[8:12])
	if size32 == 0 {
		return false, false, 0, 0, errors.New("scrcpy: frame packet carries no payload")
	}
	if size32 > maxPacketBytes {
		return false, false, 0, 0, fmt.Errorf("scrcpy: implausible frame packet size %d", size32)
	}
	return flags&packetFlagConfig != 0, flags&packetFlagKeyFrame != 0, flags & packetPTSMask, int(size32), nil
}

// Control message types, as the server's control/ControlMessage.java defines
// them. Only the messages this client sends are named.
const (
	controlInjectKeycode byte = 0
	controlInjectText    byte = 1
	controlInjectTouch   byte = 2
	controlResetVideo    byte = 17
)

// Motion event actions, as Android's MotionEvent defines them.
const (
	ActionDown byte = 0
	ActionUp   byte = 1
	ActionMove byte = 2
)

// genericFingerPointerID is SC_POINTER_ID_GENERIC_FINGER: the pointer a
// synthetic touch uses when it stands for an ordinary finger.
const genericFingerPointerID uint64 = 0xfffffffffffffffe

// primaryButton is MotionEvent.BUTTON_PRIMARY.
const primaryButton uint32 = 1

// pressureFull is a pressure of 1.0 in the u16 fixed-point form the protocol
// carries (sc_float_to_u16fp(1.0)).
const pressureFull uint16 = 0xffff

// EncodeTouch builds the 32-byte INJECT_TOUCH_EVENT message.
//
// The width and height it carries are the encoded frame size the device is
// streaming, which is the frame the coordinates are measured in
// (control_msg.c, write_position). A caller cannot pass a size of its own: the
// session supplies the size it read off the stream at session start, so a
// coordinate and the frame it was measured in cannot be assembled from two
// different sources.
func EncodeTouch(action byte, x, y, width, height int) ([]byte, error) {
	if action != ActionDown && action != ActionUp && action != ActionMove {
		return nil, fmt.Errorf("scrcpy: %d is not a touch action", action)
	}
	if width <= 0 || height <= 0 || width > maxFrameDimension || height > maxFrameDimension {
		return nil, fmt.Errorf("scrcpy: touch frame %dx%d is not a bounded render size", width, height)
	}
	if x < 0 || y < 0 || x >= width || y >= height {
		return nil, fmt.Errorf("scrcpy: touch point lies outside its %dx%d frame", width, height)
	}
	buf := make([]byte, 32)
	buf[0] = controlInjectTouch
	buf[1] = action
	binary.BigEndian.PutUint64(buf[2:10], genericFingerPointerID)
	binary.BigEndian.PutUint32(buf[10:14], uint32(int32(x)))
	binary.BigEndian.PutUint32(buf[14:18], uint32(int32(y)))
	binary.BigEndian.PutUint16(buf[18:20], uint16(width))
	binary.BigEndian.PutUint16(buf[20:22], uint16(height))
	binary.BigEndian.PutUint16(buf[22:24], pressureFull)
	binary.BigEndian.PutUint32(buf[24:28], primaryButton)
	binary.BigEndian.PutUint32(buf[28:32], primaryButton)
	return buf, nil
}

// EncodeKeyEvent builds the 14-byte INJECT_KEYCODE message.
func EncodeKeyEvent(action byte, keyCode, repeat uint32) ([]byte, error) {
	if action != ActionDown && action != ActionUp {
		return nil, fmt.Errorf("scrcpy: %d is not a key event action", action)
	}
	buf := make([]byte, 14)
	buf[0] = controlInjectKeycode
	buf[1] = action
	binary.BigEndian.PutUint32(buf[2:6], keyCode)
	binary.BigEndian.PutUint32(buf[6:10], repeat)
	binary.BigEndian.PutUint32(buf[10:14], 0)
	return buf, nil
}

// EncodeText builds an INJECT_TEXT message: the type byte, the byte length as a
// big-endian u32, then the UTF-8 bytes.
//
// Text reaches the device as typed content here, and never as command text: the
// control socket has no shell, so the value is carried as itself and the length
// is the value's own byte length.
func EncodeText(text string) ([]byte, error) {
	if text == "" {
		return nil, errors.New("scrcpy: typed text is empty")
	}
	if !utf8.ValidString(text) {
		return nil, errors.New("scrcpy: typed text is not valid UTF-8")
	}
	if len(text) > maxTextBytes {
		return nil, fmt.Errorf("scrcpy: typed text is %d bytes, over the %d byte bound", len(text), maxTextBytes)
	}
	buf := make([]byte, 5+len(text))
	buf[0] = controlInjectText
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(text)))
	copy(buf[5:], text)
	return buf, nil
}

// maxTextBytes bounds one typed text message. It is the bound the client uses
// (SC_CONTROL_MSG_INJECT_TEXT_MAX_LENGTH) and it is re-derived here rather than
// imported, so a defect in the caller's own bound cannot widen it.
const maxTextBytes = 300

// EncodeResetVideo builds the one-byte RESET_VIDEO message.
//
// The server answers it by resetting its video capture, which makes the device's
// encoder emit a fresh configuration packet and a key frame. That is the only
// way to ask this fleet's screen encoder for a key frame out of band, and it is
// what a viewer that attached mid-stream needs: without it the viewer waits for
// the next periodic IDR, and this fleet's encoder emits exactly one IDR per
// session unless it is asked.
func EncodeResetVideo() []byte { return []byte{controlResetVideo} }
