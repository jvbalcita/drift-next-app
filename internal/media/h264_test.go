package media

import (
	"bytes"
	"testing"
)

// The fixtures below are byte sequences of the shape a device actually sends:
// an SPS and a PPS that arrive once, and an IDR that follows them. The bytes
// are not a decodable picture and are not meant to be one - what these tests
// pin is the byte-level classification a live hop depends on, because a hop
// that needs a decoder to decide whether a stream is decodable cannot tell a
// black stream from a working one.

var (
	// spsFixture and ppsFixture carry four-byte start codes, which is what the
	// measured device emitted.
	spsFixture = []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x32, 0x9a, 0x02}
	ppsFixture = []byte{0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x06, 0xe2}
	// idrFixture is a key slice (NAL type 5).
	idrFixture = []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x21, 0xe2}
	// sliceFixture is a non-IDR slice (NAL type 1).
	sliceFixture = []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x9a, 0x00, 0x11}
	// seiFixture is neither, and carries a three-byte start code so both start
	// code lengths are exercised.
	seiFixture = []byte{0x00, 0x00, 0x01, 0x06, 0x05, 0x01}
)

// TestNalSpansFindsEveryUnitOfBothStartCodeLengths pins the scanner: four-byte
// and three-byte start codes in one buffer, each unit's own extent, and the
// type read from the byte after the start code.
func TestNalSpansFindsEveryUnitOfBothStartCodeLengths(t *testing.T) {
	unit := append(append(append([]byte{}, spsFixture...), seiFixture...), idrFixture...)
	spans := nalSpans(unit)
	if len(spans) != 3 {
		t.Fatalf("nalSpans found %d units, want 3: %+v", len(spans), spans)
	}
	want := []struct {
		typ   int
		start int
		end   int
	}{
		{typ: nalTypeSPS, start: 0, end: len(spsFixture)},
		{typ: 6, start: len(spsFixture), end: len(spsFixture) + len(seiFixture)},
		{typ: nalTypeIDR, start: len(spsFixture) + len(seiFixture), end: len(unit)},
	}
	for index, expected := range want {
		got := spans[index]
		if got.typ != expected.typ || got.start != expected.start || got.end != expected.end {
			t.Fatalf("unit %d = %+v, want type %d spanning [%d,%d)", index, got, expected.typ, expected.start, expected.end)
		}
	}
	// A span includes its own start code, so that a span can be copied out and
	// concatenated back into a stream another decoder can read.
	if !bytes.Equal(unit[spans[0].start:spans[0].end], spsFixture) {
		t.Fatal("the first span is not the SPS and its start code")
	}
}

// TestParameterSetsExtractsBothSets pins the extraction a hop re-attaches.
func TestParameterSetsExtractsBothSets(t *testing.T) {
	sps, pps := parameterSets(append(append([]byte{}, spsFixture...), ppsFixture...))
	if !bytes.Equal(sps, spsFixture) {
		t.Fatalf("sps = % x, want % x", sps, spsFixture)
	}
	if !bytes.Equal(pps, ppsFixture) {
		t.Fatalf("pps = % x, want % x", pps, ppsFixture)
	}
	// A stream with the sets missing reports none, which is what makes the
	// caller's decision to attach them a decision rather than a guess.
	emptySPS, emptyPPS := parameterSets(idrFixture)
	if len(emptySPS) != 0 || len(emptyPPS) != 0 {
		t.Fatalf("an IDR alone yielded sets % x / % x", emptySPS, emptyPPS)
	}
	// A buffer with only one of the two is not a pair. A hop that treated it as
	// one would attach an SPS without a PPS, and the receiver would be exactly
	// as unable to decode as it was before.
	onlySPS, onlyPPS := parameterSets(spsFixture)
	if len(onlySPS) == 0 || len(onlyPPS) != 0 {
		t.Fatalf("an SPS alone yielded % x / % x", onlySPS, onlyPPS)
	}
	if carriesParameterSets(spsFixture) {
		t.Fatal("an SPS without a PPS counts as carrying the parameter sets")
	}
}

// TestCarriesParameterSetsAndIsKeyFrame pins the two classifications publish
// branches on.
func TestCarriesParameterSetsAndIsKeyFrame(t *testing.T) {
	config := append(append([]byte{}, spsFixture...), ppsFixture...)
	if !carriesParameterSets(config) {
		t.Fatal("a buffer holding both sets does not report carrying them")
	}
	if !isKeyFrame(append(append([]byte{}, config...), idrFixture...)) {
		t.Fatal("a key slice behind the parameter sets does not report a key frame")
	}
	if isKeyFrame(sliceFixture) {
		t.Fatal("a non-IDR slice reports a key frame")
	}
	if carriesParameterSets(idrFixture) || isKeyFrame(config) {
		t.Fatal("a config packet and a picture are not distinguished")
	}
}

// TestAttachParameterSetsIsIdempotent is the behaviour that keeps a hop from
// corrupting a stream it has already fixed: the sets are prepended when they are
// missing, and never prepended twice.
func TestAttachParameterSetsIsIdempotent(t *testing.T) {
	params := append(append([]byte{}, spsFixture...), ppsFixture...)

	attached := attachParameterSets(params, idrFixture)
	if !bytes.HasPrefix(attached, params) {
		t.Fatalf("attached = % x, want the parameter sets in front", attached)
	}
	if !bytes.Equal(attached[len(params):], idrFixture) {
		t.Fatal("attaching the parameter sets changed the picture")
	}

	// Attaching again is the same value: a second pass over a stream that has
	// already been through this hop must not grow it.
	again := attachParameterSets(params, attached)
	if !bytes.Equal(again, attached) {
		t.Fatalf("a second attach produced % x, want % x", again, attached)
	}

	// A unit that carries its own sets is untouched, even when different sets
	// are offered: the unit's own sets belong with the picture, and a hop that
	// prefixed another encoder state's sets would break what it was fixing.
	ownSets := append(append([]byte{}, spsFixture...), ppsFixture...)
	otherSPS := []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x1f, 0x00, 0x01}
	otherPPS := []byte{0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x06, 0xe3}
	otherParams := append(append([]byte{}, otherSPS...), otherPPS...)
	unchanged := attachParameterSets(otherParams, ownSets)
	if !bytes.Equal(unchanged, ownSets) {
		t.Fatalf("a unit carrying its own sets was rewritten to % x", unchanged)
	}

	// With nothing tracked, a picture passes through untouched rather than
	// acquiring an empty prefix.
	if got := attachParameterSets(nil, idrFixture); !bytes.Equal(got, idrFixture) {
		t.Fatalf("a hop with no tracked sets rewrote the picture to % x", got)
	}
}
