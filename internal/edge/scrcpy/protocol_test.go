package scrcpy

import (
	"bytes"
	"encoding/binary"
	"errors"
	"slices"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
)

// headFromDevice is the 16-byte stream head this client's own configuration
// produces, captured from a lab SM-G9750: the h264 codec id, the session packet
// with its top flag bit set, then the encoded size. It is a fixture rather than
// a constructed value on purpose - the framing is the protocol, and a fixture
// that agreed with a wrong parser would agree with it forever.
var headFromDevice = []byte{
	0x68, 0x32, 0x36, 0x34, // "h264"
	0x80, 0x00, 0x00, 0x00, // session packet flags
	0x00, 0x00, 0x04, 0x38, // 1080
	0x00, 0x00, 0x08, 0xe8, // 2280
}

func TestParseStreamMetaReadsTheMeasuredHead(t *testing.T) {
	meta, err := ParseStreamMeta(headFromDevice)
	if err != nil {
		t.Fatalf("ParseStreamMeta: %v", err)
	}
	if meta.Codec != codecH264 {
		t.Fatalf("codec = %#08x, want h264", meta.Codec)
	}
	if meta.Width != 1080 || meta.Height != 2280 {
		t.Fatalf("size = %dx%d, want 1080x2280", meta.Width, meta.Height)
	}
}

func TestParseStreamMetaRefusesADeviceThatWillNotStream(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		want error
	}{
		{
			name: "capture disabled",
			head: []byte{0, 0, 0, 0, 0x80, 0, 0, 0, 0, 0, 0x04, 0x38, 0, 0, 0x08, 0xe8},
			want: ErrStreamDisabled,
		},
		{
			name: "configuration error",
			head: []byte{0, 0, 0, 1, 0x80, 0, 0, 0, 0, 0, 0x04, 0x38, 0, 0, 0x08, 0xe8},
			want: ErrStreamConfiguration,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := ParseStreamMeta(testCase.head); !errors.Is(err, testCase.want) {
				t.Fatalf("error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestParseStreamMetaRefusesAnythingElse(t *testing.T) {
	altered := func(mutate func(head []byte)) []byte {
		head := append([]byte(nil), headFromDevice...)
		mutate(head)
		return head
	}
	cases := map[string][]byte{
		"short head":           append([]byte(nil), headFromDevice[:12]...),
		"unknown codec":        altered(func(head []byte) { head[3] = '5' }),
		"missing session flag": altered(func(head []byte) { head[4] = 0 }),
		"zero width":           altered(func(head []byte) { binary.BigEndian.PutUint32(head[8:12], 0) }),
		"zero height":          altered(func(head []byte) { binary.BigEndian.PutUint32(head[12:16], 0) }),
		"unbounded width":      altered(func(head []byte) { binary.BigEndian.PutUint32(head[8:12], 10001) }),
	}
	for name, head := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseStreamMeta(head); err == nil {
				t.Fatal("a stream head that is not this protocol's was accepted")
			}
		})
	}
}

func TestParseFrameHeaderReadsTheMeasuredPackets(t *testing.T) {
	// The config packet and the first IDR as a lab SM-G9750 emitted them: the
	// config flag is bit 62 of the flag word, the key flag bit 61, and a media
	// packet carries the encoder's own PTS in the low 61 bits.
	configHeader := []byte{0x40, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x1f}
	config, key, pts, size, err := parseFrameHeader(configHeader)
	if err != nil {
		t.Fatalf("config packet: %v", err)
	}
	if !config || key || pts != 0 || size != 31 {
		t.Fatalf("config packet parsed as config=%v key=%v pts=%d size=%d", config, key, pts, size)
	}

	idrHeader := make([]byte, PacketHeaderSize)
	binary.BigEndian.PutUint64(idrHeader[0:8], packetFlagKeyFrame|22265830649)
	binary.BigEndian.PutUint32(idrHeader[8:12], 29829)
	config, key, pts, size, err = parseFrameHeader(idrHeader)
	if err != nil {
		t.Fatalf("idr packet: %v", err)
	}
	if config || !key || pts != 22265830649 || size != 29829 {
		t.Fatalf("idr parsed as config=%v key=%v pts=%d size=%d", config, key, pts, size)
	}

	mediaHeader := make([]byte, PacketHeaderSize)
	binary.BigEndian.PutUint64(mediaHeader[0:8], 22265930649)
	binary.BigEndian.PutUint32(mediaHeader[8:12], 5857)
	_, key, pts, size, err = parseFrameHeader(mediaHeader)
	if err != nil {
		t.Fatalf("media packet: %v", err)
	}
	if key || pts != 22265930649 || size != 5857 {
		t.Fatalf("media packet parsed as key=%v pts=%d size=%d", key, pts, size)
	}
}

func TestParseFrameHeaderRefusesAMalformedHeader(t *testing.T) {
	cases := map[string][]byte{
		"short header":     make([]byte, 8),
		"a session packet": {0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x1f},
		"an empty payload": {0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		"an absurd length": {0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0xff, 0xff},
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, _, _, err := parseFrameHeader(header); err == nil {
				t.Fatal("a header that is not this protocol's was accepted")
			}
		})
	}
}

// TestEncodeTouchMatchesTheProtocol pins the 32-byte layout of
// INJECT_TOUCH_EVENT against the byte offsets the client's own serializer uses
// (app/src/control_msg.c, which writes the position, then the pressure, then the
// action button and the buttons).
func TestEncodeTouchMatchesTheProtocol(t *testing.T) {
	message, err := EncodeTouch(ActionDown, 540, 1140, 1080, 2280)
	if err != nil {
		t.Fatalf("EncodeTouch: %v", err)
	}
	want := []byte{
		2,                                              // INJECT_TOUCH_EVENT
		0,                                              // ACTION_DOWN
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xfe, // generic finger pointer id
		0x00, 0x00, 0x02, 0x1c, // x = 540
		0x00, 0x00, 0x04, 0x74, // y = 1140
		0x04, 0x38, // width = 1080
		0x08, 0xe8, // height = 2280
		0xff, 0xff, // pressure 1.0
		0x00, 0x00, 0x00, 0x01, // action button: primary
		0x00, 0x00, 0x00, 0x01, // buttons: primary
	}
	if len(message) != 32 {
		t.Fatalf("message is %d bytes, want 32", len(message))
	}
	if !bytes.Equal(message, want) {
		t.Fatalf("message = % x\nwant      % x", message, want)
	}
}

func TestEncodeTouchRefusesACoordinateOutsideItsFrame(t *testing.T) {
	cases := []struct {
		name          string
		x, y          int
		width, height int
	}{
		{name: "x at the frame edge", x: 1080, y: 10, width: 1080, height: 2280},
		{name: "y at the frame edge", x: 10, y: 2280, width: 1080, height: 2280},
		{name: "negative", x: -1, y: 10, width: 1080, height: 2280},
		{name: "no frame", x: 10, y: 10, width: 0, height: 0},
		{name: "unbounded frame", x: 10, y: 10, width: 10001, height: 2280},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := EncodeTouch(ActionMove, testCase.x, testCase.y, testCase.width, testCase.height); err == nil {
				t.Fatal("a coordinate outside its frame was encoded")
			}
		})
	}
	if _, err := EncodeTouch(9, 10, 10, 1080, 2280); err == nil {
		t.Fatal("an unknown touch action was encoded")
	}
}

func TestEncodeTextCarriesTheValuesOwnLength(t *testing.T) {
	message, err := EncodeText("hello world")
	if err != nil {
		t.Fatalf("EncodeText: %v", err)
	}
	want := append([]byte{1, 0, 0, 0, 11}, []byte("hello world")...)
	if !bytes.Equal(message, want) {
		t.Fatalf("message = % x\nwant      % x", message, want)
	}
	// A value with a multi-byte rune is carried by its byte length, not its rune
	// count: the length field is what the device reads to know how much to take.
	message, err = EncodeText("é")
	if err != nil {
		t.Fatalf("EncodeText: %v", err)
	}
	if binary.BigEndian.Uint32(message[1:5]) != 2 || len(message) != 7 {
		t.Fatalf("é encoded with length %d in %d bytes", binary.BigEndian.Uint32(message[1:5]), len(message))
	}
}

func TestEncodeTextRefusesWhatItCannotCarry(t *testing.T) {
	if _, err := EncodeText(""); err == nil {
		t.Fatal("empty text was encoded")
	}
	if _, err := EncodeText(string(bytes.Repeat([]byte{'a'}, maxTextBytes+1))); err == nil {
		t.Fatal("text over the bound was encoded")
	}
	if _, err := EncodeText(string([]byte{0xff, 0xfe})); err == nil {
		t.Fatal("invalid UTF-8 was encoded")
	}
}

func TestEncodeKeyEventMatchesTheProtocol(t *testing.T) {
	message, err := EncodeKeyEvent(ActionDown, 4, 0)
	if err != nil {
		t.Fatalf("EncodeKeyEvent: %v", err)
	}
	want := []byte{0, 0, 0, 0, 0, 4, 0, 0, 0, 0, 0, 0, 0, 0}
	if len(message) != 14 || !bytes.Equal(message, want) {
		t.Fatalf("message = % x, want % x", message, want)
	}
	if _, err := EncodeKeyEvent(ActionMove, 4, 0); err == nil {
		t.Fatal("a motion action was encoded as a key event")
	}
}

// TestEncodeResetVideoIsTheOneByteMessage pins the message a viewer that
// attached mid-stream relies on: the server treats it as an empty message, so a
// single type byte with a payload behind it would be read as the next message's
// type.
func TestEncodeResetVideoIsTheOneByteMessage(t *testing.T) {
	if message := EncodeResetVideo(); len(message) != 1 || message[0] != 17 {
		t.Fatalf("reset video = % x, want 11", message)
	}
}

func TestSwipeStepsAreBounded(t *testing.T) {
	cases := map[time.Duration]int{
		10 * time.Millisecond:  2,
		200 * time.Millisecond: 6,
		2 * time.Second:        maxSwipeSteps,
		10 * time.Minute:       maxSwipeSteps,
	}
	for duration, want := range cases {
		if got := swipeSteps(duration); got != want {
			t.Fatalf("swipeSteps(%s) = %d, want %d", duration, got, want)
		}
	}
}

func TestSessionIDSourcesAreBoundedAndNamedAlike(t *testing.T) {
	id, err := sessionID(func() (uint32, error) { return 0x81234567, nil })
	if err != nil {
		t.Fatalf("sessionID: %v", err)
	}
	if id != 0x01234567 {
		t.Fatalf("id = %#08x, want the top bit cleared", id)
	}
	if got := sessionSocketName(id); got != "scrcpy_01234567" {
		t.Fatalf("socket name = %q, want scrcpy_01234567", got)
	}
	// The name the tunnel registers and the id the server is launched with are
	// both built from this one value, and the device names its socket after the
	// id it was given: a disagreement between them is a handshake that never
	// completes, with the device waiting on a socket nobody registered.
	tunnel, err := adb.MirrorAbstractSocketName(id)
	if err != nil {
		t.Fatalf("MirrorAbstractSocketName: %v", err)
	}
	if tunnel != "localabstract:scrcpy_01234567" {
		t.Fatalf("tunnel target = %q, want the session's own socket", tunnel)
	}
	launch, err := adb.MirrorServerLaunchArgv(id, "info", false)
	if err != nil {
		t.Fatalf("MirrorServerLaunchArgv: %v", err)
	}
	if !slices.Contains(launch, "scid=01234567") {
		t.Fatalf("launch %q does not carry the same session id the tunnel registered", launch)
	}
	if _, err := sessionID(func() (uint32, error) { return 0, nil }); err == nil {
		t.Fatal("a zero session id was accepted")
	}
	if _, err := sessionID(func() (uint32, error) { return 0, errors.New("no source") }); err == nil {
		t.Fatal("a failing source did not fail the session")
	}
}
