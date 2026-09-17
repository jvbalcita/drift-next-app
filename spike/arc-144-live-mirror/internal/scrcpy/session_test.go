package scrcpy

import (
	"encoding/binary"
	"testing"
)

// The expected bytes are the ones scrcpy's own serializer produces for the same
// message (app/tests/test_control_msg_serialize.c, INJECT_TOUCH_EVENT), except
// for the two tail fields, which scrcpy's client sets to BUTTON_PRIMARY for a
// touch. A wrong byte here is a tap that lands somewhere else or not at all, so
// the layout is asserted rather than assumed.
func TestEncodeTouchMatchesProtocol(t *testing.T) {
	got := EncodeTouch(ActionDown, 100, 200, 1080, 1920)
	want := []byte{
		ControlInjectTouch,                             // type
		0x00,                                           // ACTION_DOWN
		0x12, 0x34, 0x56, 0x78, 0x87, 0x65, 0x43, 0x21, // pointer id -- replaced below
		0x00, 0x00, 0x00, 0x64, // x = 100
		0x00, 0x00, 0x00, 0xc8, // y = 200
		0x04, 0x38, // width 1080
		0x07, 0x80, // height 1920
		0xff, 0xff, // pressure 1.0
		0x00, 0x00, 0x00, 0x01, // action button = primary
		0x00, 0x00, 0x00, 0x01, // buttons = primary
	}
	binary.BigEndian.PutUint64(want[2:10], GenericFingerPointerID)
	if len(got) != 32 {
		t.Fatalf("encoded %d bytes, want 32", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d = %#02x, want %#02x\n got % x\nwant % x", i, got[i], want[i], got, want)
		}
	}
}

func TestEncodeTouchActionUpDiffers(t *testing.T) {
	down := EncodeTouch(ActionDown, 540, 1140, 1080, 2280)
	up := EncodeTouch(ActionUp, 540, 1140, 1080, 2280)
	if down[1] != 0 || up[1] != 1 {
		t.Fatalf("actions encoded as %d/%d, want 0/1", down[1], up[1])
	}
}

func TestParsePacketHeader(t *testing.T) {
	// A key frame (bit 61) carrying PTS 0x1FFFFFFFFFFFFF and 1234 bytes.
	pts := uint64(0x1FFFFFFFFFFFFF)
	hdr := make([]byte, PacketHeaderSize)
	hdr[0] = 0x20 | byte((pts>>56)&0x1f) // key flag, top bits of the PTS
	for i := 1; i < 8; i++ {
		hdr[i] = byte(pts >> uint(8*(7-i)))
	}
	binary.BigEndian.PutUint32(hdr[8:12], 1234)

	key, config, gotPTS, size, err := ParsePacketHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if !key || config {
		t.Fatalf("key=%v config=%v, want key only", key, config)
	}
	if gotPTS != pts {
		t.Fatalf("pts %#x, want %#x", gotPTS, pts)
	}
	if size != 1234 {
		t.Fatalf("size %d, want 1234", size)
	}
}

// The config packet's header is the all-flags form: bit 62 set, PTS zero, no
// key-frame bit (Streamer.writeFrameMeta sets ptsAndFlags = PACKET_FLAG_CONFIG).
func TestParsePacketHeaderConfigPacket(t *testing.T) {
	hdr := make([]byte, PacketHeaderSize)
	hdr[0] = 0x40
	binary.BigEndian.PutUint32(hdr[8:12], 40)
	key, config, pts, size, err := ParsePacketHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if key || !config || pts != 0 || size != 40 {
		t.Fatalf("key=%v config=%v pts=%d size=%d, want config-only, pts 0, size 40", key, config, pts, size)
	}
}

func TestParsePacketHeaderRejectsSessionPacket(t *testing.T) {
	hdr := make([]byte, PacketHeaderSize)
	hdr[0] = 0x80 // session packet: bit 63, as Streamer.writeSessionMeta writes it
	if _, _, _, _, err := ParsePacketHeader(hdr); err == nil {
		t.Fatal("a session packet was accepted as a media packet")
	}
}
