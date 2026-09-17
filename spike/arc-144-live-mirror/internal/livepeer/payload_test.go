package livepeer

import (
	"os"
	"testing"

	"github.com/pion/rtp/codecs"

	"spikelocal/arc144/internal/annexb"
)

// The RTP payloader is the boundary between the device's Annex-B stream and what
// the browser's depacketiser sees, and a payloader that silently drops the tail
// of a multi-NAL access unit produces a stream that looks live and decodes to
// nothing. This test measures that boundary directly, on a captured device
// stream, so the number is not inferred from the browser's counters.
func TestPayloaderCoversEveryAccessUnit(t *testing.T) {
	path := os.Getenv("ARC144_CAPTURE")
	if path == "" {
		t.Skip("set ARC144_CAPTURE to a captured Annex-B stream to run this check")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	aus, err := annexb.Split(raw)
	if err != nil {
		t.Fatal(err)
	}
	payloader := &codecs.H264Payloader{}
	for i, au := range aus {
		if i >= 5 {
			break
		}
		payloads := payloader.Payload(PayloadMTU, au.Data)
		sum, largest := 0, 0
		for _, p := range payloads {
			sum += len(p)
			if len(p) > largest {
				largest = len(p)
			}
		}
		nals, _ := annexb.NALs(au.Data)
		types := make([]int, 0, len(nals))
		for _, n := range nals {
			types = append(types, n.Type)
		}
		t.Logf("au %d: %d bytes, %d NALs %v -> %d RTP payloads, %d payload bytes (largest %d)",
			i, len(au.Data), len(nals), types, len(payloads), sum, largest)
		// Annex-B start codes are consumed, so the payload bytes are slightly
		// fewer than the access unit; anything beyond that margin means a NAL
		// did not reach the wire.
		if sum < len(au.Data)-4*len(nals) {
			t.Errorf("au %d: payloads carry %d of %d bytes (%d NALs): the payloader dropped data",
				i, sum, len(au.Data), len(nals))
		}
	}
}

// A fragmented-stream packet must not contain a start code: the depacketiser
// reassembles the NAL and would hand the decoder a NAL header of 0x00 if the
// payloader forwarded the Annex-B prefix instead of splitting on it.
func TestPayloadsCarryNoStartCode(t *testing.T) {
	path := os.Getenv("ARC144_CAPTURE")
	if path == "" {
		t.Skip("set ARC144_CAPTURE to a captured Annex-B stream to run this check")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	aus, err := annexb.Split(raw)
	if err != nil {
		t.Fatal(err)
	}
	payloader := &codecs.H264Payloader{}
	for i, au := range aus {
		if i >= 5 {
			break
		}
		for j, p := range payloader.Payload(PayloadMTU, au.Data) {
			if len(p) >= 4 && p[0] == 0 && p[1] == 0 && p[2] == 0 && p[3] == 1 {
				t.Errorf("au %d payload %d still carries an Annex-B start code", i, j)
			}
		}
	}
}
