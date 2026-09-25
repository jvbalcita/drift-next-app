package media

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	MirrorControlChannelLabel = "drift-control-v1"
	MirrorControlMessageSize  = 72
	MirrorControlVersion      = 1
	MirrorControlCurrentVersion = 2
	MirrorControlMaxMessageSize = 16 * 1024
	MirrorControlMaxFollowers = 64
	MirrorControlMaxFollowerIDBytes = 128
)

type MirrorControlEventKind uint8

const (
	MirrorControlTouchDown   MirrorControlEventKind = 1
	MirrorControlTouchMove   MirrorControlEventKind = 2
	MirrorControlTouchUp     MirrorControlEventKind = 3
	MirrorControlTouchCancel MirrorControlEventKind = 4
	MirrorControlKey         MirrorControlEventKind = 5
	MirrorControlSetFollowers MirrorControlEventKind = 6
)

const (
	MirrorControlGestureUnspecified uint8 = 0
	MirrorControlGestureTap         uint8 = 1
	MirrorControlGestureSwipe       uint8 = 2
	MirrorControlMinSwipeDurationMS uint32 = 16
	MirrorControlMaxSwipeDurationMS uint32 = 10_000
)

type MirrorControlMessage struct {
	Version     uint8
	Kind        MirrorControlEventKind
	Final       bool
	Sequence    uint64
	GestureID   uint64
	TimestampMS int64
	Generation  uint64
	Width       uint32
	Height      uint32
	X           uint32
	Y           uint32
	KeyCode     uint32
	Repeat      uint32
	GestureKind uint8
	DurationMS  uint32
	FollowerDeviceIDs []string
}

// MirrorControlBinding is the application-owned authority a single selected
// viewing must prove before its DataChannel can carry input. A viewing claim
// alone is not authority to control a device.
type MirrorControlBinding struct {
	WorkspaceID  string
	DeviceID     string
	SessionID    string
	LeaseID      string
	HolderID     string
	FencingToken uint64
	ActorType    string
	ActorID      string
}

// MirrorControlPeer is the only part of a selected viewing the application
// needs to arm input. The concrete Pion peer satisfies it.
type MirrorControlPeer interface {
	Stats() StreamStats
	ViewingClaimMatches(MirrorViewingClaim) bool
	BindControl(uint64, MirrorControlHandler) error
	ControlDone() <-chan struct{}
	RevokeControl()
}

// MirrorControlReporter optionally sends a bounded result to the operator over
// the same already-authorized channel. It is deliberately separate from the
// binding interface so video-only peers cannot accidentally gain a write path.
type MirrorControlReporter interface {
	SendControlReport([]byte) error
}

func DecodeMirrorControlMessage(data []byte) (MirrorControlMessage, error) {
	if len(data) < MirrorControlMessageSize || len(data) > MirrorControlMaxMessageSize {
		return MirrorControlMessage{}, fmt.Errorf("media: control message is %d bytes, want %d..%d", len(data), MirrorControlMessageSize, MirrorControlMaxMessageSize)
	}
	version := data[0]
	if version != MirrorControlVersion && version != MirrorControlCurrentVersion {
		return MirrorControlMessage{}, fmt.Errorf("media: control message version %d is not supported", data[0])
	}
	reserved := [][]byte{data[3:8]}
	if version == MirrorControlVersion {
		reserved = append(reserved, data[64:72])
	} else {
		reserved = append(reserved, data[69:72])
	}
	for _, bytes := range reserved {
		for _, value := range bytes {
			if value != 0 {
				return MirrorControlMessage{}, errors.New("media: control message reserved bytes must be zero")
			}
		}
	}
	message := MirrorControlMessage{
		Version:     version,
		Kind:        MirrorControlEventKind(data[1]),
		Final:       data[2]&1 != 0,
		Sequence:    binary.BigEndian.Uint64(data[8:16]),
		GestureID:   binary.BigEndian.Uint64(data[16:24]),
		TimestampMS: int64(binary.BigEndian.Uint64(data[24:32])),
		Generation:  binary.BigEndian.Uint64(data[32:40]),
		Width:       binary.BigEndian.Uint32(data[40:44]),
		Height:      binary.BigEndian.Uint32(data[44:48]),
		X:           binary.BigEndian.Uint32(data[48:52]),
		Y:           binary.BigEndian.Uint32(data[52:56]),
		KeyCode:     binary.BigEndian.Uint32(data[56:60]),
		Repeat:      binary.BigEndian.Uint32(data[60:64]),
	}
	if version == MirrorControlCurrentVersion {
		message.DurationMS = binary.BigEndian.Uint32(data[64:68])
		message.GestureKind = data[68]
		if message.Kind == MirrorControlSetFollowers {
			followers, err := decodeMirrorControlFollowers(data[MirrorControlMessageSize:])
			if err != nil {
				return MirrorControlMessage{}, err
			}
			message.FollowerDeviceIDs = followers
		} else if len(data) != MirrorControlMessageSize {
			return MirrorControlMessage{}, errors.New("media: only a follower-selection message may carry a variable payload")
		}
	} else if len(data) != MirrorControlMessageSize {
		return MirrorControlMessage{}, errors.New("media: legacy control messages must use the fixed frame size")
	}
	if err := message.Validate(); err != nil {
		return MirrorControlMessage{}, err
	}
	return message, nil
}

func (m MirrorControlMessage) Validate() error {
	if m.Sequence == 0 || m.Generation == 0 || m.TimestampMS < 0 {
		return errors.New("media: control message requires a sequence, stream generation and non-negative timestamp")
	}
	switch m.Kind {
	case MirrorControlTouchDown, MirrorControlTouchMove, MirrorControlTouchUp, MirrorControlTouchCancel:
		if m.GestureID == 0 || m.Width == 0 || m.Height == 0 || m.Width > 10000 || m.Height > 10000 || m.X >= m.Width || m.Y >= m.Height {
			return errors.New("media: touch control message requires a gesture and a point inside a bounded render frame")
		}
		terminal := m.Kind == MirrorControlTouchUp || m.Kind == MirrorControlTouchCancel
		if m.Final != terminal {
			return errors.New("media: only touch-up and touch-cancel may be terminal")
		}
		if m.KeyCode != 0 || m.Repeat != 0 {
			return errors.New("media: touch control message must not carry a key")
		}
		if m.Version >= MirrorControlCurrentVersion {
			if m.Kind == MirrorControlTouchUp {
				if m.GestureKind != MirrorControlGestureTap && m.GestureKind != MirrorControlGestureSwipe {
					return errors.New("media: a terminal touch event must state its follower gesture kind")
				}
				if m.GestureKind == MirrorControlGestureTap && m.DurationMS != 0 {
					return errors.New("media: a tap must not carry a swipe duration")
				}
				if m.GestureKind == MirrorControlGestureSwipe && (m.DurationMS < MirrorControlMinSwipeDurationMS || m.DurationMS > MirrorControlMaxSwipeDurationMS) {
					return errors.New("media: a swipe duration must be between 16 and 10000 milliseconds")
				}
			} else if m.GestureKind != MirrorControlGestureUnspecified || m.DurationMS != 0 {
				return errors.New("media: only a terminal touch-up may carry a follower gesture kind or duration")
			}
		} else if m.GestureKind != MirrorControlGestureUnspecified || m.DurationMS != 0 || len(m.FollowerDeviceIDs) != 0 {
			return errors.New("media: legacy control messages must not carry follower metadata")
		}
	case MirrorControlKey:
		if !m.Final || m.GestureID != 0 || m.Width != 0 || m.Height != 0 || m.X != 0 || m.Y != 0 || m.KeyCode == 0 || m.KeyCode > 10000 || m.Repeat != 1 || m.GestureKind != MirrorControlGestureUnspecified || m.DurationMS != 0 || len(m.FollowerDeviceIDs) != 0 {
			return errors.New("media: key control message requires one terminal key event and no touch payload")
		}
	case MirrorControlSetFollowers:
		if m.Version < MirrorControlCurrentVersion || m.Final || m.GestureID != 0 || m.Width != 0 || m.Height != 0 || m.X != 0 || m.Y != 0 || m.KeyCode != 0 || m.Repeat != 0 || m.GestureKind != MirrorControlGestureUnspecified || m.DurationMS != 0 {
			return errors.New("media: follower selection requires a version 2 control message with no input payload")
		}
		if err := validateMirrorControlFollowers(m.FollowerDeviceIDs); err != nil {
			return err
		}
	default:
		return fmt.Errorf("media: control event kind %d is not supported", m.Kind)
	}
	return nil
}

func decodeMirrorControlFollowers(data []byte) ([]string, error) {
	if len(data) < 2 {
		return nil, errors.New("media: follower selection is missing its bounded count")
	}
	count := int(binary.BigEndian.Uint16(data[:2]))
	if count > MirrorControlMaxFollowers {
		return nil, fmt.Errorf("media: follower selection names %d devices, maximum is %d", count, MirrorControlMaxFollowers)
	}
	followers := make([]string, 0, count)
	offset := 2
	for index := 0; index < count; index++ {
		if len(data)-offset < 2 {
			return nil, errors.New("media: follower selection ended before a device id length")
		}
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		offset += 2
		if length == 0 || length > MirrorControlMaxFollowerIDBytes || len(data)-offset < length {
			return nil, errors.New("media: follower selection contains an empty, oversized or incomplete device id")
		}
		followers = append(followers, string(data[offset:offset+length]))
		offset += length
	}
	if offset != len(data) {
		return nil, errors.New("media: follower selection has trailing bytes")
	}
	if err := validateMirrorControlFollowers(followers); err != nil {
		return nil, err
	}
	return followers, nil
}

func validateMirrorControlFollowers(followers []string) error {
	if len(followers) > MirrorControlMaxFollowers {
		return fmt.Errorf("media: follower selection names %d devices, maximum is %d", len(followers), MirrorControlMaxFollowers)
	}
	for _, id := range followers {
		if id == "" || len(id) > MirrorControlMaxFollowerIDBytes || !utf8.ValidString(id) || strings.TrimSpace(id) != id {
			return errors.New("media: follower selection contains an invalid device id")
		}
		for _, value := range id {
			if value < 0x20 || value == 0x7f {
				return errors.New("media: follower selection contains an invalid device id")
			}
		}
	}
	return nil
}

// MirrorInput maps a validated realtime event into the live session's closed
// input vocabulary. It performs no authorization: callers may use the result
// only after the application kernel accepted and revalidated the binding that
// delivered this message.
func (m MirrorControlMessage) MirrorInput() (MirrorInput, error) {
	if err := m.Validate(); err != nil {
		return MirrorInput{}, err
	}
	input := MirrorInput{
		X:           int(m.X),
		Y:           int(m.Y),
		FrameWidth:  int(m.Width),
		FrameHeight: int(m.Height),
		KeyCode:     m.KeyCode,
		Repeat:      m.Repeat,
	}
	switch m.Kind {
	case MirrorControlTouchDown:
		input.Kind = MirrorInputTouchDown
	case MirrorControlTouchMove:
		input.Kind = MirrorInputTouchMove
	case MirrorControlTouchUp:
		input.Kind = MirrorInputTouchUp
	case MirrorControlTouchCancel:
		input.Kind = MirrorInputTouchCancel
	case MirrorControlKey:
		input.Kind = MirrorInputKeyEvent
	default:
		return MirrorInput{}, fmt.Errorf("media: control event kind %d has no mirror input", m.Kind)
	}
	return input, nil
}

type MirrorControlSequence struct {
	generation uint64
	last       uint64
	gesture    uint64
	terminal   bool
}

func NewMirrorControlSequence(generation uint64) (*MirrorControlSequence, error) {
	if generation == 0 {
		return nil, errors.New("media: control sequence requires a stream generation")
	}
	return &MirrorControlSequence{generation: generation}, nil
}

func (s *MirrorControlSequence) Accept(message MirrorControlMessage) error {
	if s == nil {
		return errors.New("media: control sequence is not constructed")
	}
	if err := message.Validate(); err != nil {
		return err
	}
	if message.Generation != s.generation {
		return errors.New("media: control message names a stale stream generation")
	}
	if message.Sequence <= s.last {
		return errors.New("media: control message is out of order")
	}
	if message.Kind == MirrorControlKey {
		s.last = message.Sequence
		return nil
	}
	switch message.Kind {
	case MirrorControlSetFollowers:
		s.last = message.Sequence
		return nil
	case MirrorControlTouchDown:
		if s.gesture != 0 && !s.terminal {
			return errors.New("media: a touch gesture is already active")
		}
		s.gesture, s.terminal = message.GestureID, false
	case MirrorControlTouchMove:
		if s.gesture == 0 || s.terminal || message.GestureID != s.gesture {
			return errors.New("media: touch move does not belong to the active gesture")
		}
	case MirrorControlTouchUp, MirrorControlTouchCancel:
		if s.gesture == 0 || s.terminal || message.GestureID != s.gesture {
			return errors.New("media: terminal touch event does not belong to the active gesture")
		}
		s.terminal = true
	}
	s.last = message.Sequence
	return nil
}
