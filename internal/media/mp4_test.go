package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// The fragmented MP4 container the TCP transport carries. These cases assert the
// bytes a decoder reads, not that a writer returned nil: a container that is
// well-formed enough to accept and wrong enough to decode to nothing is this
// fleet's black-screen failure in another shape.

// testSPS and testPPS are a synthetic but well-shaped pair: a baseline SPS with
// profile 0x42, compatibility 0x00 and level 0x32, and a PPS. Nothing here
// decodes them; the cases are about which bytes end up where.
var (
	testSPS = []byte{0x67, 0x42, 0x00, 0x32, 0xac, 0xd0, 0x0a, 0x0f, 0x11, 0x22, 0x33}
	testPPS = []byte{0x68, 0xce, 0x3c, 0x80, 0x44, 0x55}
)

// annexB builds an access unit from NAL units, each behind a four-byte start
// code, the way the device's stream frames them.
func annexB(nals ...[]byte) []byte {
	var out []byte
	for _, nal := range nals {
		out = append(out, 0, 0, 0, 1)
		out = append(out, nal...)
	}
	return out
}

// lengthPrefixed is the same access unit in the form an avc1 sample holds.
func lengthPrefixed(nals ...[]byte) []byte {
	var out []byte
	for _, nal := range nals {
		out = binary.BigEndian.AppendUint32(out, uint32(len(nal)))
		out = append(out, nal...)
	}
	return out
}

func idrFrame(t *testing.T) []byte {
	t.Helper()
	return annexB(testSPS, testPPS, []byte{0x65, 0x88, 0x84, 0x21, 0xff})
}

func deltaFrame(payload byte) []byte {
	return annexB([]byte{0x41, 0x9a, payload, 0x7f})
}

// parsedBox is one ISO box found in a buffer: its type and its body, without the
// header.
type parsedBox struct {
	typ  string
	body []byte
}

// parseBoxes walks a buffer's top-level boxes and fails when they do not add up
// to the buffer exactly - a size that disagrees with its content is the failure
// a decoder reports as nothing at all.
func parseBoxes(t *testing.T, data []byte) []parsedBox {
	t.Helper()
	var boxes []parsedBox
	index := 0
	for index < len(data) {
		if index+8 > len(data) {
			t.Fatalf("a box header is cut short at byte %d of %d", index, len(data))
		}
		size := int(binary.BigEndian.Uint32(data[index : index+4]))
		typ := string(data[index+4 : index+8])
		if size < 8 || index+size > len(data) {
			t.Fatalf("box %q at byte %d declares size %d, which does not fit in %d bytes", typ, index, size, len(data))
		}
		boxes = append(boxes, parsedBox{typ: typ, body: data[index+8 : index+size]})
		index += size
	}
	return boxes
}

// child finds the first child box of the given type inside a box body.
func child(t *testing.T, parent parsedBox, typ string) parsedBox {
	t.Helper()
	for _, box := range parseBoxes(t, parent.body) {
		if box.typ == typ {
			return box
		}
	}
	t.Fatalf("box %q carries no %q", parent.typ, typ)
	return parsedBox{}
}

// childAt finds a child box inside a container whose first bytes are not boxes:
// a full box's version, flags and leading count come before its children.
func childAt(t *testing.T, parent parsedBox, offset int, typ string) parsedBox {
	t.Helper()
	if offset > len(parent.body) {
		t.Fatalf("box %q is too short to carry a %q at offset %d", parent.typ, typ, offset)
	}
	for _, box := range parseBoxes(t, parent.body[offset:]) {
		if box.typ == typ {
			return box
		}
	}
	t.Fatalf("box %q carries no %q", parent.typ, typ)
	return parsedBox{}
}

// embeddedBox finds a box inside a record that is not a box container at all -
// a sample entry, whose fixed fields come first and whose codec box follows.
func embeddedBox(t *testing.T, parent parsedBox, typ string) parsedBox {
	t.Helper()
	index := bytes.Index(parent.body, []byte(typ))
	if index < 4 {
		t.Fatalf("box %q carries no %q", parent.typ, typ)
	}
	start := index - 4
	size := int(binary.BigEndian.Uint32(parent.body[start:index]))
	if size < 8 || start+size > len(parent.body) {
		t.Fatalf("the %q box inside %q declares size %d of %d bytes", typ, parent.typ, size, len(parent.body))
	}
	return parsedBox{typ: typ, body: parent.body[index+4 : start+size]}
}

// writeStream writes one stream's frames through a muxer and returns the bytes.
func writeStream(t *testing.T, width, height int, frames []struct {
	unit []byte
	key  bool
	pts  uint64
}) ([]byte, *MP4Writer) {
	t.Helper()
	var out bytes.Buffer
	writer, err := NewMP4Writer(&out, width, height)
	if err != nil {
		t.Fatalf("NewMP4Writer: %v", err)
	}
	for index, frame := range frames {
		if err := writer.WriteAccessUnit(frame.unit, frame.key, frame.pts); err != nil {
			t.Fatalf("WriteAccessUnit(frame %d): %v", index, err)
		}
	}
	return out.Bytes(), writer
}

type testFrame = struct {
	unit []byte
	key  bool
	pts  uint64
}

// TestTheContainerDeclaresTheCodecOnceAndDescribesTheFrameSize is the
// initialisation segment's whole job: a decoder that reads it knows the codec,
// its profile and level, and the size the pictures are encoded at - which is
// also the coordinate frame an operator's input is measured in.
func TestTheContainerDeclaresTheCodecOnceAndDescribesTheFrameSize(t *testing.T) {
	data, _ := writeStream(t, 1080, 2280, []testFrame{
		{unit: idrFrame(t), key: true, pts: 1000},
	})
	boxes := parseBoxes(t, data)
	if len(boxes) < 2 || boxes[0].typ != "ftyp" || boxes[1].typ != "moov" {
		t.Fatalf("a stream must open with ftyp then moov, and it opened with %v", boxTypes(boxes))
	}
	if !bytes.Contains(boxes[0].body, []byte("avc1")) || !bytes.Contains(boxes[0].body, []byte("isom")) {
		t.Fatalf("the file type box does not declare the brands a browser accepts: % x", boxes[0].body)
	}

	trak := child(t, boxes[1], "trak")
	mdia := child(t, trak, "mdia")
	minf := child(t, mdia, "minf")
	stbl := child(t, minf, "stbl")
	// The sample description is a full box: its version, flags and entry count
	// come before the sample entry itself.
	stsd := child(t, stbl, "stsd")
	avc1 := childAt(t, stsd, 8, "avc1")
	// The codec declaration is embedded in the sample entry's fixed record.
	avcC := embeddedBox(t, avc1, "avcC")
	// The size sits two 16-bit fields before the codec box, behind the
	// compressor name the sample entry carries.
	offset := bytes.Index(avc1.body, []byte("avcC")) - 4
	if got := int(binary.BigEndian.Uint16(avc1.body[offset-54 : offset-52])); got != 1080 {
		t.Errorf("the sample entry declares width %d, want 1080", got)
	}
	if got := int(binary.BigEndian.Uint16(avc1.body[offset-52 : offset-50])); got != 2280 {
		t.Errorf("the sample entry declares height %d, want 2280", got)
	}

	if got := avcC.body[0]; got != 1 {
		t.Errorf("AVCDecoderConfigurationRecord version is %d, want 1", got)
	}
	if got := avcC.body[1:4]; !bytes.Equal(got, testSPS[1:4]) {
		t.Errorf("the record declares profile/compatibility/level % x, want the SPS's own % x", got, testSPS[1:4])
	}
	if got := avcC.body[4]; got != 0xff {
		t.Errorf("the sample length prefix byte is %#x, want 0xff (a four-byte length)", got)
	}
	if got := avcC.body[5] & 0x1f; got != 1 {
		t.Errorf("the record declares %d sequence parameter sets, want 1", got)
	}
	spsLength := int(binary.BigEndian.Uint16(avcC.body[6:8]))
	if got := avcC.body[8 : 8+spsLength]; !bytes.Equal(got, testSPS) {
		t.Errorf("the record carries SPS % x, want % x", got, testSPS)
	}
	ppsCount := avcC.body[8+spsLength]
	if ppsCount != 1 {
		t.Fatalf("the record declares %d picture parameter sets, want 1", ppsCount)
	}
	ppsLength := int(binary.BigEndian.Uint16(avcC.body[9+spsLength : 11+spsLength]))
	if got := avcC.body[11+spsLength : 11+spsLength+ppsLength]; !bytes.Equal(got, testPPS) {
		t.Errorf("the record carries PPS % x, want % x", got, testPPS)
	}

	// The movie extends box is what makes this a fragmented movie rather than
	// one whose samples were supposed to be in an empty table.
	trex := child(t, child(t, boxes[1], "mvex"), "trex")
	if got := binary.BigEndian.Uint32(trex.body[4:8]); got != mp4TrackID {
		t.Errorf("the track extends box names track %d, want %d", got, mp4TrackID)
	}
}

// TestEveryStreamStartsWithAKeyFrame: a container whose first sample is a delta
// frame decodes to nothing and reports nothing. The refusal happens before a
// single byte is written, so a consumer cannot be handed a stream that will
// never produce a picture.
func TestEveryStreamStartsWithAKeyFrame(t *testing.T) {
	var out bytes.Buffer
	writer, err := NewMP4Writer(&out, 1080, 2280)
	if err != nil {
		t.Fatalf("NewMP4Writer: %v", err)
	}
	err = writer.WriteAccessUnit(deltaFrame(0x11), false, 1000)
	if !errors.Is(err, ErrMP4FirstFrameNotKey) {
		t.Fatalf("a stream that opens on a delta frame was accepted: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("a refused first picture still wrote %d bytes", out.Len())
	}
	if writer.Started() {
		t.Fatal("a refused first picture left the writer believing a stream had started")
	}
}

// TestAStreamWithNoParameterSetsIsRefused: without the codec's parameter sets
// there is no sample entry to write and no codec for a decoder to use.
func TestAStreamWithNoParameterSetsIsRefused(t *testing.T) {
	var out bytes.Buffer
	writer, err := NewMP4Writer(&out, 1080, 2280)
	if err != nil {
		t.Fatalf("NewMP4Writer: %v", err)
	}
	bareIDR := annexB([]byte{0x65, 0x88, 0x84})
	if err := writer.WriteAccessUnit(bareIDR, true, 1000); !errors.Is(err, ErrMP4NoParameterSets) {
		t.Fatalf("a key frame with no parameter sets was accepted: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("a refused stream still wrote %d bytes", out.Len())
	}
}

// TestEveryPictureIsItsOwnFragmentAndItsSampleIsLengthPrefixed asserts the two
// properties a browser's source buffer depends on: a fragment is self-contained
// (its sample offset is relative to its own moof), and the sample is the avc1
// form of the access unit with the parameter sets left to the declaration.
func TestEveryPictureIsItsOwnFragmentAndItsSampleIsLengthPrefixed(t *testing.T) {
	idr := idrFrame(t)
	delta := deltaFrame(0x42)
	frames := []testFrame{
		{unit: idr, key: true, pts: 1_000_000},
		{unit: delta, key: false, pts: 1_033_000},
		{unit: delta, key: false, pts: 1_066_000},
	}
	data, writer := writeStream(t, 1080, 2280, frames)

	boxes := parseBoxes(t, data)
	if len(boxes) != 2+2*len(frames) {
		t.Fatalf("a stream of %d pictures produced boxes %v, want ftyp, moov and one moof+mdat per picture", len(frames), boxTypes(boxes))
	}

	wantSample := map[bool][]byte{
		true:  lengthPrefixed([]byte{0x65, 0x88, 0x84, 0x21, 0xff}),
		false: lengthPrefixed([]byte{0x41, 0x9a, 0x42, 0x7f}),
	}
	// The first picture has no predecessor, so it states the interval this
	// fleet's encoder runs at; every later one states the device's own delta.
	wantTimes := []uint64{0, nominalFrameDurationUS, nominalFrameDurationUS + 33_000}
	wantDurations := []uint32{nominalFrameDurationUS, 33_000, 33_000}
	for index, frame := range frames {
		moof := boxes[2+2*index]
		mdat := boxes[3+2*index]
		if moof.typ != "moof" || mdat.typ != "mdat" {
			t.Fatalf("picture %d produced %v, want a moof followed by its mdat", index, []string{moof.typ, mdat.typ})
		}
		mfhd := child(t, moof, "mfhd")
		if got := binary.BigEndian.Uint32(mfhd.body[4:8]); got != uint32(index+1) {
			t.Errorf("picture %d carries fragment sequence number %d, want %d", index, got, index+1)
		}
		traf := child(t, moof, "traf")
		tfhd := child(t, traf, "tfhd")
		flags := binary.BigEndian.Uint32(tfhd.body[0:4]) & 0xffffff
		if flags&tfhdDefaultBaseIsMoof == 0 {
			t.Errorf("picture %d's tfhd flags %#x do not make its offsets relative to its own fragment", index, flags)
		}
		if got := binary.BigEndian.Uint32(tfhd.body[4:8]); got != mp4TrackID {
			t.Errorf("picture %d names track %d, want %d", index, got, mp4TrackID)
		}
		tfdt := child(t, traf, "tfdt")
		if got := tfdt.body[0]; got != 1 {
			t.Errorf("picture %d's decode time box is version %d, want version 1 for a 64-bit time", index, got)
		}
		if got := binary.BigEndian.Uint64(tfdt.body[4:12]); got != wantTimes[index] {
			t.Errorf("picture %d carries decode time %d, want %d", index, got, wantTimes[index])
		}
		trun := child(t, traf, "trun")
		if got := binary.BigEndian.Uint32(trun.body[4:8]); got != 1 {
			t.Fatalf("picture %d's fragment carries %d samples, want exactly 1", index, got)
		}
		dataOffset := int(int32(binary.BigEndian.Uint32(trun.body[8:12])))
		if want := bodyLength(moof) + 8; dataOffset != want {
			t.Errorf("picture %d's sample offset is %d, want %d (past this moof and the mdat header)", index, dataOffset, want)
		}
		if got := int(binary.BigEndian.Uint32(trun.body[16:20])); got != len(mdat.body) {
			t.Errorf("picture %d declares a sample of %d bytes but carries %d", index, got, len(mdat.body))
		}
		if got := int(binary.BigEndian.Uint32(trun.body[12:16])); got != int(wantDurations[index]) {
			t.Errorf("picture %d declares duration %d, want %d", index, got, wantDurations[index])
		}
		flags = binary.BigEndian.Uint32(trun.body[20:24])
		if frame.key && flags != mp4FlagsKeyFrame {
			t.Errorf("picture %d is a key frame and declares flags %#x, want %#x", index, flags, mp4FlagsKeyFrame)
		}
		if !frame.key && flags != mp4FlagsDeltaFrame {
			t.Errorf("picture %d is a delta frame and declares flags %#x, want %#x", index, flags, mp4FlagsDeltaFrame)
		}
		if got := mdat.body; !bytes.Equal(got, wantSample[frame.key]) {
			t.Errorf("picture %d's sample is % x, want % x", index, got, wantSample[frame.key])
		}
		if bytes.Contains(mdat.body, testSPS) || bytes.Contains(mdat.body, testPPS) {
			t.Errorf("picture %d's sample repeats the parameter sets the initialisation segment declares", index)
		}
	}
	if writer.Frames() != uint64(len(frames)) || writer.KeyFrames() != 1 {
		t.Errorf("the writer reports %d frames and %d key frames, want %d and 1", writer.Frames(), writer.KeyFrames(), len(frames))
	}
	if writer.BytesWritten() != uint64(len(data)) {
		t.Errorf("the writer reports %d bytes written but the buffer holds %d", writer.BytesWritten(), len(data))
	}
	if !bytes.Equal(writer.InitSegment(), data[:len(writer.InitSegment())]) {
		t.Error("the initialisation segment the writer reports is not the one at the head of the stream")
	}
}

// TestTheDecodeTimelineFollowsTheDevicesOwnCadence: the durations are the PTS
// deltas the device sent, not an assumed frame rate. This fleet's encoder emits
// a picture only when the screen changes, so the real interval IS the duration.
func TestTheDecodeTimelineFollowsTheDevicesOwnCadence(t *testing.T) {
	delta := deltaFrame(0x42)
	frames := []testFrame{
		{unit: idrFrame(t), key: true, pts: 5_000_000},
		{unit: delta, key: false, pts: 5_100_000},
		{unit: delta, key: false, pts: 5_150_000},
	}
	data, _ := writeStream(t, 1080, 2280, frames)
	boxes := parseBoxes(t, data)

	wantTimes := []uint64{0, nominalFrameDurationUS, nominalFrameDurationUS + 100_000}
	wantDurations := []uint32{nominalFrameDurationUS, 100_000, 50_000}
	for index := range frames {
		traf := child(t, boxes[2+2*index], "traf")
		gotTime := binary.BigEndian.Uint64(child(t, traf, "tfdt").body[4:12])
		if gotTime != wantTimes[index] {
			t.Errorf("picture %d carries decode time %d, want %d", index, gotTime, wantTimes[index])
		}
		gotDuration := binary.BigEndian.Uint32(child(t, traf, "trun").body[12:16])
		if gotDuration != wantDurations[index] {
			t.Errorf("picture %d declares duration %d, want the device's own interval %d", index, gotDuration, wantDurations[index])
		}
	}
}

// TestTheTimelineAdvancesWhenTheDeviceRestartsItsClock: an encoder reset restarts
// the device's PTS, and a fragment timeline that went backwards with it would be
// a container a decoder has to reject. The timeline advances by the previous
// interval instead.
func TestTheTimelineAdvancesWhenTheDeviceRestartsItsClock(t *testing.T) {
	delta := deltaFrame(0x42)
	frames := []testFrame{
		{unit: idrFrame(t), key: true, pts: 1_000_000},
		{unit: delta, key: false, pts: 1_040_000},
		{unit: delta, key: false, pts: 0}, // the encoder restarted its clock
		{unit: delta, key: false, pts: 40_000},
	}
	data, _ := writeStream(t, 1080, 2280, frames)
	boxes := parseBoxes(t, data)

	var times []uint64
	for index := range frames {
		traf := child(t, boxes[2+2*index], "traf")
		times = append(times, binary.BigEndian.Uint64(child(t, traf, "tfdt").body[4:12]))
	}
	for index := 1; index < len(times); index++ {
		if times[index] < times[index-1] {
			t.Fatalf("the decode timeline went backwards at picture %d: %v", index, times)
		}
	}
	if times[3] == 0 {
		t.Fatalf("a restarted device clock reset the timeline to zero: %v", times)
	}
}

// TestAReconfigurationMidStreamIsRefused: the initialisation segment has already
// declared the codec. Carrying samples the declaration no longer describes is a
// stream a decoder cannot follow, so it is refused rather than carried.
func TestAReconfigurationMidStreamIsRefused(t *testing.T) {
	otherSPS := []byte{0x67, 0x4d, 0x00, 0x2a, 0xab, 0xcd, 0x12, 0x34, 0x56, 0x78}
	var out bytes.Buffer
	writer, err := NewMP4Writer(&out, 1080, 2280)
	if err != nil {
		t.Fatalf("NewMP4Writer: %v", err)
	}
	if err := writer.WriteAccessUnit(idrFrame(t), true, 1000); err != nil {
		t.Fatalf("WriteAccessUnit: %v", err)
	}
	written := out.Len()
	if err := writer.WriteAccessUnit(annexB(otherSPS, testPPS, []byte{0x65, 0x88}), true, 40_000); !errors.Is(err, ErrMP4CodecChanged) {
		t.Fatalf("a mid-stream codec change was carried: %v", err)
	}
	if out.Len() != written {
		t.Fatalf("a refused reconfiguration still wrote %d bytes", out.Len()-written)
	}
}

// TestAnAccessUnitWithNoPictureIsRefused: a configuration-only unit is not a
// picture, and writing one as a fragment is exactly the unit a decoder draws
// nothing from. Nothing is written for either refusal.
func TestAnAccessUnitWithNoPictureIsRefused(t *testing.T) {
	var out bytes.Buffer
	writer, err := NewMP4Writer(&out, 1080, 2280)
	if err != nil {
		t.Fatalf("NewMP4Writer: %v", err)
	}
	if err := writer.WriteAccessUnit(annexB(testSPS, testPPS), true, 1000); !errors.Is(err, ErrMP4NoPicture) {
		t.Fatalf("a unit carrying only the parameter sets was accepted as the stream's first picture: %v", err)
	}
	if err := writer.WriteAccessUnit([]byte{0x01, 0x02, 0x03}, true, 1000); !errors.Is(err, ErrMP4NoPicture) {
		t.Fatalf("a unit with no NAL unit in it was accepted: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("a refused picture still wrote %d bytes", out.Len())
	}
}

// TestAWriterWithoutADestinationOrASizeIsNotAStream: a stream nobody can
// assemble, or one whose frames were never measured, is refused at construction
// rather than written blind.
func TestAWriterWithoutADestinationOrASizeIsNotAStream(t *testing.T) {
	if _, err := NewMP4Writer(nil, 1080, 2280); !errors.Is(err, ErrMP4NotConstructed) {
		t.Fatalf("a writer with no destination was constructed: %v", err)
	}
	if _, err := NewMP4Writer(io.Discard, 0, 2280); err == nil {
		t.Fatal("a writer with no frame size was constructed")
	}
}

// TestAWriteThatFailsIsReported keeps the one failure that would otherwise be
// silent: a consumer that stopped reading is not a stream that was delivered.
func TestAWriteThatFailsIsReported(t *testing.T) {
	writer, err := NewMP4Writer(failingWriter{}, 1080, 2280)
	if err != nil {
		t.Fatalf("NewMP4Writer: %v", err)
	}
	if err := writer.WriteAccessUnit(idrFrame(t), true, 1000); err == nil {
		t.Fatal("a stream whose destination refused every byte reported success")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no reader") }

func boxTypes(boxes []parsedBox) []string {
	out := make([]string, 0, len(boxes))
	for _, box := range boxes {
		out = append(out, box.typ)
	}
	return out
}

// bodyLength reports a box's size as it appears on the wire: its header
// included.
func bodyLength(box parsedBox) int { return 8 + len(box.body) }
