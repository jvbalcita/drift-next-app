// The container the TCP compatibility transport carries.
//
// ARC-143 ships two transports. WebRTC carries H.264 over RTP natively and needs
// no container at all; the second transport is a byte stream a browser plays
// through Media Source Extensions, and MSE does not accept a raw H.264
// elementary stream - it accepts fragmented MP4 (ISO/IEC 14496-12). This file is
// that container, and only that: it turns one device's Annex-B access units into
// an initialisation segment plus one movie fragment per picture, and it knows
// nothing about devices, sessions or HTTP.
//
// A fragment per PICTURE, not per key-frame interval. A fragment is the smallest
// unit a browser's source buffer can be handed, so one fragment per IDR interval
// would hold every picture until the next IDR - two seconds on this fleet
// (i-frame-interval:int=2) - which is several times the compatibility
// transport's whole budget. One fragment per picture costs a moof each (about a
// hundred bytes) and keeps the buffered picture current.
//
// Three properties are what make it correct rather than merely well-formed, and
// each exists because the failure it prevents is silent:
//
//   - The stream must START at a key frame. A fragment timeline whose first
//     sample is a delta frame decodes to nothing and reports nothing, which is
//     this fleet's black-screen trap in another container.
//   - The parameter sets are declared once, in the initialisation segment's
//     avcC - the place a decoder reads them - and are stripped from the samples.
//     They are also refused if they CHANGE mid-stream: an initialisation segment
//     that no longer describes its samples is a stream a decoder cannot follow,
//     and saying so is the only alternative to showing nothing.
//   - The composition timeline never goes backwards. A fragment's decode time is
//     a monotonic timeline derived from the device's own PTS deltas, so an
//     encoder that restarts its clock mid-session advances the timeline by the
//     previous interval instead of producing a file a decoder must reject.
package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	// mp4Timescale is the track's media timescale. The device's own timestamps
	// are microseconds, so a PTS is already a duration in this timescale: no
	// conversion happens on the way through, and nothing is rounded.
	mp4Timescale = 1_000_000

	// mp4TrackID is the single video track's identity inside the movie.
	mp4TrackID = 1

	// mp4MajorBrand, mp4MinorVersion and mp4CompatibleBrands are the classic
	// isom/iso2/avc1/mp41 combination - the shape every MSE implementation in
	// the target engines accepts, and the shape ffmpeg's own fragmented output
	// carries.
	mp4MajorBrand   = "isom"
	mp4MinorVersion = 512

	// tfhdDefaultBaseIsMoof is the 'tfhd' flag that makes a fragment's sample
	// offsets relative to its own moof, so a fragment carries everything it
	// needs and a source buffer never has to remember where it was appended.
	tfhdDefaultBaseIsMoof = 0x020000

	// trunDataOffsetPresent, trunSampleDurationPresent, trunSampleSizePresent
	// and trunSampleFlagsPresent: a fragment states each of its samples in full
	// rather than relying on the initialisation segment's defaults, because a
	// stream whose samples are described nowhere a decoder can find is the same
	// silent failure in another shape.
	trunDataOffsetPresent     = 0x000001
	trunSampleDurationPresent = 0x000100
	trunSampleSizePresent     = 0x000200
	trunSampleFlagsPresent    = 0x000400

	// mp4FlagsKeyFrame and mp4FlagsDeltaFrame are sample flags in the layout
	// 'trun' and 'trex' share: two bits of sample_depends_on and one bit saying
	// the sample is not a sync sample. A decoder may start at a sync sample and
	// nowhere else.
	mp4FlagsKeyFrame   = uint32(0x02000000) // sample_depends_on = 2: depends on nothing
	mp4FlagsDeltaFrame = uint32(0x01010000) // depends on others, and is not a sync sample

	// mp4UnityMatrix is the 3x3 transform every track in this product carries:
	// no rotation, no scaling, unit cells in 16.16 fixed point.
	mp4UnityMatrix = uint32(0x00010000)
)

var (
	// ErrMP4FirstFrameNotKey reports an access unit offered as the first picture
	// of a stream that a decoder could not start from.
	ErrMP4FirstFrameNotKey = errors.New("media: a fragmented MP4 stream starts at a key frame and this is not one")

	// ErrMP4NoParameterSets reports a stream whose codec configuration has not
	// been seen. An avc1 sample entry without an avcC describes nothing, so
	// there is no initialisation segment to write and no stream to carry.
	ErrMP4NoParameterSets = errors.New("media: a fragmented MP4 stream needs the codec's parameter sets before its first picture")

	// ErrMP4CodecChanged reports a device that re-sent different parameter sets
	// mid-stream. The initialisation segment already declared the old ones, and
	// hand-holding a decoder through a reconfiguration this container cannot
	// express is not something a hop may pretend it did.
	ErrMP4CodecChanged = errors.New("media: the device's encoder changed its parameter sets mid-stream, so the stream's declaration no longer describes its samples")

	// ErrMP4NotConstructed reports a writer with no destination or no frame size.
	ErrMP4NotConstructed = errors.New("media: the fragmented MP4 writer is not constructed")

	// ErrMP4NoPicture reports an access unit whose NAL units carry no picture: a
	// configuration-only unit, or one with no NAL unit in it at all. Writing one
	// as a fragment is exactly the unit a decoder draws nothing from.
	ErrMP4NoPicture = errors.New("media: an access unit with no picture in it is not a frame")
)

// MP4Writer writes one live session as fragmented MP4.
//
// It is not safe for concurrent use: one writer belongs to one stream's own
// serving goroutine, which is also what keeps a fragment's boxes contiguous on
// the wire.
type MP4Writer struct {
	writer        io.Writer
	width, height int

	params []byte
	header []byte

	started  bool
	seenPTS  bool
	lastPTS  uint64
	lastDur  uint32
	timeline uint64
	sequence uint32
	frames   uint64
	keys     uint64
	written  uint64
}

// NewMP4Writer builds a writer for one stream, at the size its frames are
// encoded at. The size is part of the initialisation segment: a sample entry
// that states the wrong size is a picture a browser scales, and a coordinate
// frame nobody can verify.
func NewMP4Writer(writer io.Writer, width, height int) (*MP4Writer, error) {
	if writer == nil {
		return nil, ErrMP4NotConstructed
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("media: a fragmented MP4 stream needs the size its frames are encoded at, not %dx%d", width, height)
	}
	return &MP4Writer{
		writer:  writer,
		width:   width,
		height:  height,
		lastDur: nominalFrameDurationUS,
	}, nil
}

// InitSegment reports the initialisation segment this writer wrote, or nil
// before it has written one. It is what a caller re-sends to a reader that
// attached later, and it is a copy: nothing here hands out a buffer it may write
// into again.
func (m *MP4Writer) InitSegment() []byte {
	if m == nil || len(m.header) == 0 {
		return nil
	}
	return append([]byte(nil), m.header...)
}

// Frames, KeyFrames and BytesWritten are the numbers a stream reports about
// itself.
func (m *MP4Writer) Frames() uint64 {
	if m == nil {
		return 0
	}
	return m.frames
}

func (m *MP4Writer) KeyFrames() uint64 {
	if m == nil {
		return 0
	}
	return m.keys
}

func (m *MP4Writer) BytesWritten() uint64 {
	if m == nil {
		return 0
	}
	return m.written
}

// Started reports whether the initialisation segment has been written, which is
// to say whether this stream is a stream a browser can be handed at all.
func (m *MP4Writer) Started() bool {
	return m != nil && m.started
}

// WriteAccessUnit writes one picture as its own fragment.
//
// The first unit of a stream must be a key frame and must carry (or have been
// preceded by) the parameter sets: anything else produces a stream that decodes
// to nothing. Later units are checked for a mid-stream reconfiguration, which it
// refuses rather than carrying a stream whose declaration is stale.
func (m *MP4Writer) WriteAccessUnit(unit []byte, key bool, ptsUS uint64) error {
	if m == nil || m.writer == nil || m.width <= 0 || m.height <= 0 {
		return ErrMP4NotConstructed
	}
	if len(unit) == 0 {
		return errors.New("media: an access unit with no bytes is not a picture")
	}
	sps, pps := parameterSets(unit)
	carries := len(sps) > 0 && len(pps) > 0

	if !m.started && !key {
		return ErrMP4FirstFrameNotKey
	}
	// The picture is assembled and validated before a byte is written, so a unit
	// this stream cannot describe leaves no partial stream behind.
	sample, err := mp4Sample(unit)
	if err != nil {
		return err
	}

	if !m.started {
		params := m.params
		if carries {
			params = append(append([]byte(nil), sps...), pps...)
		}
		if len(params) == 0 {
			return ErrMP4NoParameterSets
		}
		avcC, err := avcCFromParameterSets(params)
		if err != nil {
			return err
		}
		m.params = params
		m.header = mp4InitSegment(m.width, m.height, avcC)
		if err := m.write(m.header); err != nil {
			return err
		}
		m.started = true
	} else if carries {
		if !bytes.Equal(append(append([]byte(nil), sps...), pps...), m.params) {
			return ErrMP4CodecChanged
		}
	}

	base := m.timeline
	duration := m.step(ptsUS)
	fragment := mp4Fragment(m.sequence+1, base, duration, key, len(sample))
	if err := m.write(fragment); err != nil {
		return err
	}
	if err := m.write(mp4Box("mdat", sample)); err != nil {
		return err
	}

	m.sequence++
	m.timeline = base + uint64(duration)
	m.frames++
	if key {
		m.keys++
	}
	return nil
}

// step reports how long the picture that is about to be written lasts, and
// advances the timeline past it.
//
// A picture's duration is the interval since the previous one, which is the
// device's own cadence while its clock runs forward - this fleet's encoder emits
// a frame only when the screen changes, so the interval is real time and not a
// frame rate. When the device's clock goes backwards (an encoder restart resets
// it) the timeline advances by the previous interval rather than backwards: a
// fragment's decode time is a monotonic timeline, and a container whose timeline
// reverses is a container a decoder must reject.
func (m *MP4Writer) step(ptsUS uint64) uint32 {
	duration := m.lastDur
	if m.seenPTS && ptsUS > m.lastPTS {
		delta := ptsUS - m.lastPTS
		if delta > math.MaxUint32 {
			delta = math.MaxUint32
		}
		duration = uint32(delta)
	}
	m.lastPTS = ptsUS
	m.seenPTS = true
	m.lastDur = duration
	return duration
}

func (m *MP4Writer) write(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	written, err := m.writer.Write(data)
	m.written += uint64(written)
	if err != nil {
		return fmt.Errorf("media: the fragmented MP4 stream could not be written: %w", err)
	}
	if written != len(data) {
		return fmt.Errorf("media: the fragmented MP4 stream was written short (%d of %d bytes)", written, len(data))
	}
	return nil
}

// avcCFromParameterSets builds the AVCDecoderConfigurationRecord an avc1 sample
// entry carries: the profile, the length prefix every sample uses, and the
// parameter sets themselves.
//
// It is the one place the codec is declared, and a decoder that has this has
// everything it needs to start - which is why the record is written before the
// first picture rather than beside it.
func avcCFromParameterSets(params []byte) ([]byte, error) {
	var sps, pps [][]byte
	for _, span := range nalSpans(params) {
		nal := nalPayload(params, span)
		switch span.typ {
		case nalTypeSPS:
			if len(nal) > 0 {
				sps = append(sps, nal)
			}
		case nalTypePPS:
			if len(nal) > 0 {
				pps = append(pps, nal)
			}
		}
	}
	if len(sps) == 0 || len(pps) == 0 {
		return nil, ErrMP4NoParameterSets
	}
	if len(sps) > 31 || len(pps) > 255 {
		return nil, fmt.Errorf("media: a codec configuration with %d SPS and %d PPS is not one this stream can declare", len(sps), len(pps))
	}
	if len(sps[0]) < 4 {
		return nil, errors.New("media: the sequence parameter set is too short to declare a profile")
	}
	out := []byte{
		1,                     // configurationVersion
		sps[0][1],             // AVCProfileIndication
		sps[0][2],             // profile_compatibility
		sps[0][3],             // AVCLevelIndication
		0xff,                  // reserved (6 bits) and lengthSizeMinusOne = 3, a 4-byte sample length
		0xe0 | byte(len(sps)), // reserved (3 bits) and numOfSequenceParameterSets
	}
	for _, set := range sps {
		out = binary.BigEndian.AppendUint16(out, uint16(len(set)))
		out = append(out, set...)
	}
	out = append(out, byte(len(pps)))
	for _, set := range pps {
		out = binary.BigEndian.AppendUint16(out, uint16(len(set)))
		out = append(out, set...)
	}
	return out, nil
}

// mp4Sample converts one Annex-B access unit into the length-prefixed form an
// avc1 sample holds, dropping the parameter sets: they are declared once in the
// initialisation segment, which is where a decoder reads them, and a sample that
// carried a second copy would be declaring the codec twice.
func mp4Sample(unit []byte) ([]byte, error) {
	spans := nalSpans(unit)
	if len(spans) == 0 {
		return nil, ErrMP4NoPicture
	}
	out := make([]byte, 0, len(unit))
	for _, span := range spans {
		if span.typ == nalTypeSPS || span.typ == nalTypePPS {
			continue
		}
		nal := nalPayload(unit, span)
		if len(nal) == 0 {
			continue
		}
		out = binary.BigEndian.AppendUint32(out, uint32(len(nal)))
		out = append(out, nal...)
	}
	if len(out) == 0 {
		return nil, ErrMP4NoPicture
	}
	return out, nil
}

// nalPayload reports a NAL unit's bytes without its start code.
func nalPayload(unit []byte, span nalSpan) []byte {
	start := span.start
	for start < span.end && unit[start] == 0 {
		start++
	}
	if start < span.end && unit[start] == 1 {
		start++
	}
	if start >= span.end {
		return nil
	}
	return unit[start:span.end]
}

// mp4Fragment builds one movie fragment that carries exactly one sample.
//
// The data offset is computed from the boxes' own sizes rather than patched into
// a finished buffer: a fragment whose offset is one byte out decodes to nothing,
// and the arithmetic is small enough to be right the first time.
func mp4Fragment(sequence uint32, baseMediaDecodeTime uint64, duration uint32, key bool, size int) []byte {
	mfhd := mp4FullBox("mfhd", 0, 0, mp4U32(sequence))
	tfhd := mp4FullBox("tfhd", 0, tfhdDefaultBaseIsMoof, mp4U32(mp4TrackID))
	tfdt := mp4FullBox("tfdt", 1, 0, mp4U64(baseMediaDecodeTime))

	const trunSize = 8 + 4 + 20 // header, version and flags, five 32-bit sample fields
	trafSize := 8 + len(tfhd) + len(tfdt) + trunSize
	moofSize := 8 + len(mfhd) + trafSize
	// The sample begins after this moof and after the mdat's own header.
	dataOffset := moofSize + 8

	flags := mp4FlagsDeltaFrame
	if key {
		flags = mp4FlagsKeyFrame
	}
	trun := mp4FullBox("trun", 0,
		trunDataOffsetPresent|trunSampleDurationPresent|trunSampleSizePresent|trunSampleFlagsPresent,
		mp4Concat(
			mp4U32(1),                  // sample_count
			mp4U32(uint32(dataOffset)), // data_offset
			mp4U32(duration),
			mp4U32(uint32(size)),
			mp4U32(flags),
		),
	)
	traf := mp4Box("traf", mp4Concat(tfhd, tfdt, trun))
	return mp4Box("moof", mp4Concat(mfhd, traf))
}

// mp4InitSegment builds the segment a browser's source buffer is given first:
// the brands, the movie header, the one video track's sample description (which
// carries the codec declaration), and the movie-extends box that says the
// samples themselves arrive as fragments.
func mp4InitSegment(width, height int, avcC []byte) []byte {
	matrix := mp4Concat(
		mp4U32(mp4UnityMatrix), mp4U32(0), mp4U32(0),
		mp4U32(0), mp4U32(mp4UnityMatrix), mp4U32(0),
		mp4U32(0), mp4U32(0), mp4U32(0x40000000),
	)

	ftyp := mp4Box("ftyp", mp4Concat(
		[]byte(mp4MajorBrand),
		mp4U32(mp4MinorVersion),
		[]byte("isom"), []byte("iso2"), []byte("avc1"), []byte("mp41"),
	))

	mvhd := mp4FullBox("mvhd", 0, 0, mp4Concat(
		mp4U32(0), mp4U32(0), // creation_time, modification_time
		mp4U32(mp4Timescale), mp4U32(0), // timescale, duration (the fragments carry it)
		mp4U32(0x00010000),        // rate
		mp4U16(0x0100), mp4U16(0), // volume, reserved
		mp4U32(0), mp4U32(0), // reserved
		matrix,
		mp4U32(0), mp4U32(0), mp4U32(0), mp4U32(0), mp4U32(0), mp4U32(0), // pre_defined
		mp4U32(mp4TrackID+1), // next_track_ID
	))

	tkhd := mp4FullBox("tkhd", 0, 0x000003, mp4Concat(
		mp4U32(0), mp4U32(0), // creation_time, modification_time
		mp4U32(mp4TrackID), mp4U32(0), mp4U32(0), // track_ID, reserved, duration
		mp4U32(0), mp4U32(0), // reserved
		mp4I16(0), mp4I16(0), // layer, alternate_group
		mp4I16(0), mp4U16(0), // volume (0 for video), reserved
		matrix,
		mp4U32(uint32(width)<<16), mp4U32(uint32(height)<<16),
	))

	mdhd := mp4FullBox("mdhd", 0, 0, mp4Concat(
		mp4U32(0), mp4U32(0),
		mp4U32(mp4Timescale), mp4U32(0),
		mp4U16(0x55c4), mp4U16(0), // language 'und', pre_defined
	))

	hdlr := mp4FullBox("hdlr", 0, 0, mp4Concat(
		mp4U32(0), []byte("vide"), mp4U32(0), mp4U32(0), mp4U32(0), []byte("drift\x00"),
	))

	vmhd := mp4FullBox("vmhd", 0, 1, mp4Concat(mp4U16(0), mp4U16(0), mp4U16(0), mp4U16(0)))
	// An avc1 sample entry: the reserved fields every sample entry carries, the
	// size the frames are encoded at (which is the coordinate frame an operator's
	// input is measured in), and the codec declaration.
	avc1 := mp4Concat(
		make([]byte, 6), // reserved
		mp4U16(1),       // data_reference_index
		mp4U16(0), mp4U16(0),
		mp4U32(0), mp4U32(0), mp4U32(0),
		mp4U16(uint16(width)), mp4U16(uint16(height)),
		mp4U32(0x00480000), mp4U32(0x00480000), mp4U32(0), // 72 dpi, reserved
		mp4U16(1), // frame_count
		mp4CompressorName("drift"),
		mp4U16(0x0018), mp4I16(-1), // depth, pre_defined
		mp4Box("avcC", avcC),
	)
	stsd := mp4FullBox("stsd", 0, 0, mp4Concat(mp4U32(1), mp4Box("avc1", avc1)))
	// The sample tables are empty and present: in a fragmented movie every sample
	// is described by the fragment that carries it, and a decoder that finds no
	// table at all has a sample entry it cannot use.
	stts := mp4FullBox("stts", 0, 0, mp4U32(0))
	stsc := mp4FullBox("stsc", 0, 0, mp4U32(0))
	stsz := mp4FullBox("stsz", 0, 0, mp4Concat(mp4U32(0), mp4U32(0)))
	stco := mp4FullBox("stco", 0, 0, mp4U32(0))
	stbl := mp4Box("stbl", mp4Concat(stsd, stts, stsc, stsz, stco))

	dref := mp4FullBox("dref", 0, 0, mp4Concat(mp4U32(1), mp4FullBox("url ", 0, 1, nil)))
	dinf := mp4Box("dinf", dref)
	minf := mp4Box("minf", mp4Concat(vmhd, dinf, stbl))
	mdia := mp4Box("mdia", mp4Concat(mdhd, hdlr, minf))
	trak := mp4Box("trak", mp4Concat(tkhd, mdia))

	trex := mp4FullBox("trex", 0, 0, mp4Concat(
		mp4U32(mp4TrackID), mp4U32(1), mp4U32(0), mp4U32(0), mp4U32(0),
	))
	mvex := mp4Box("mvex", trex)
	moov := mp4Box("moov", mp4Concat(mvhd, trak, mvex))

	return mp4Concat(ftyp, moov)
}

// mp4CompressorName is the 32-byte Pascal-style name a sample entry carries.
func mp4CompressorName(name string) []byte {
	out := make([]byte, 32)
	length := len(name)
	if length > 31 {
		length = 31
	}
	out[0] = byte(length)
	copy(out[1:], name[:length])
	return out
}

// mp4Box wraps a payload in an ISO box: a 32-bit size that includes the header,
// then the four-character type.
func mp4Box(typ string, payload []byte) []byte {
	out := make([]byte, 0, 8+len(payload))
	out = binary.BigEndian.AppendUint32(out, uint32(8+len(payload)))
	out = append(out, typ...)
	return append(out, payload...)
}

// mp4FullBox wraps a payload in a box that carries a version and flags.
func mp4FullBox(typ string, version byte, flags uint32, payload []byte) []byte {
	head := []byte{version, byte(flags >> 16), byte(flags >> 8), byte(flags)}
	return mp4Box(typ, append(head, payload...))
}

func mp4Concat(parts ...[]byte) []byte {
	total := 0
	for _, part := range parts {
		total += len(part)
	}
	out := make([]byte, 0, total)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func mp4U64(value uint64) []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, value)
	return out
}

func mp4U32(value uint32) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, value)
	return out
}

func mp4U16(value uint16) []byte {
	out := make([]byte, 2)
	binary.BigEndian.PutUint16(out, value)
	return out
}

func mp4I16(value int16) []byte {
	return mp4U16(uint16(value))
}
