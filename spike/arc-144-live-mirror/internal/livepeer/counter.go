package livepeer

import (
	"sync"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
)

// rtpCounter counts the RTP packets this hop emits, as an interceptor.
//
// It exists because "the browser decoded nothing" has to be attributable to a
// side: the number of packets the sender produced and the number the browser
// received disagreed by a factor of three while every higher-level counter
// looked healthy, and no browser-side statistic can distinguish "we did not send
// it" from "it did not arrive".
type rtpCounter struct {
	mu       sync.Mutex
	packets  int
	bytes    int
	markers  int
	firstSeq uint16
	lastSeq  uint16
	seqGaps  int
	head     []PacketSnapshot
	ssrc     uint32
	pt       uint8
}

// PacketSnapshot is one outbound packet's header, kept for the first few packets.
type PacketSnapshot struct {
	Seq    uint16 `json:"seq"`
	TS     uint32 `json:"ts"`
	Marker bool   `json:"marker"`
	PT     uint8  `json:"payload_type"`
	SSRC   uint32 `json:"ssrc"`
	Bytes  int    `json:"bytes"`
}

// Counts is what the sender emitted on the wire.
type Counts struct {
	Packets  int              `json:"packets"`
	Bytes    int              `json:"bytes"`
	Markers  int              `json:"markers"`
	SeqGaps  int              `json:"sequence_gaps"`
	FirstSeq uint16           `json:"first_seq"`
	LastSeq  uint16           `json:"last_seq"`
	SSRC     uint32           `json:"ssrc"`
	PT       uint8            `json:"payload_type"`
	Head     []PacketSnapshot `json:"head"`
}

func (c *rtpCounter) observe(h *rtp.Header, payloadBytes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.packets == 0 {
		c.firstSeq, c.ssrc, c.pt = h.SequenceNumber, h.SSRC, h.PayloadType
	} else if h.SequenceNumber != c.lastSeq+1 {
		c.seqGaps++
	}
	c.lastSeq = h.SequenceNumber
	c.packets++
	c.bytes += payloadBytes
	if h.Marker {
		c.markers++
	}
	if len(c.head) < 12 {
		c.head = append(c.head, PacketSnapshot{
			Seq: h.SequenceNumber, TS: h.Timestamp, Marker: h.Marker,
			PT: h.PayloadType, SSRC: h.SSRC, Bytes: payloadBytes,
		})
	}
}

func (c *rtpCounter) snapshot() Counts {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Counts{
		Packets: c.packets, Bytes: c.bytes, Markers: c.markers, SeqGaps: c.seqGaps,
		FirstSeq: c.firstSeq, LastSeq: c.lastSeq, SSRC: c.ssrc, PT: c.pt,
		Head: c.head,
	}
}

// factory builds the interceptor the registry installs.
type factory struct{ counter *rtpCounter }

func (f *factory) NewInterceptor(id string) (interceptor.Interceptor, error) {
	_ = id
	return &countInterceptor{counter: f.counter}, nil
}

type countInterceptor struct{ counter *rtpCounter }

func (i *countInterceptor) BindLocalStream(info *interceptor.StreamInfo, next interceptor.RTPWriter) interceptor.RTPWriter {
	i.counter.mu.Lock()
	i.counter.ssrc = info.SSRC
	i.counter.mu.Unlock()
	return &countWriter{counter: i.counter, next: next}
}

// A pass-through remote-stream binding leaves the default reader in place.
func (i *countInterceptor) BindRemoteStream(info *interceptor.StreamInfo, next interceptor.RTPReader) interceptor.RTPReader {
	_ = info
	return next
}

// RTCP is passed through untouched: this interceptor measures RTP only.
func (i *countInterceptor) BindRTCPReader(reader interceptor.RTCPReader) interceptor.RTCPReader {
	return reader
}

func (i *countInterceptor) BindRTCPWriter(writer interceptor.RTCPWriter) interceptor.RTCPWriter {
	return writer
}

func (i *countInterceptor) UnbindLocalStream(info *interceptor.StreamInfo)  { _ = info }
func (i *countInterceptor) UnbindRemoteStream(info *interceptor.StreamInfo) { _ = info }
func (i *countInterceptor) Close() error                                    { return nil }

type countWriter struct {
	counter *rtpCounter
	next    interceptor.RTPWriter
}

func (w *countWriter) Write(h *rtp.Header, payload []byte, attributes interceptor.Attributes) (int, error) {
	w.counter.observe(h, len(payload))
	return w.next.Write(h, payload, attributes)
}
