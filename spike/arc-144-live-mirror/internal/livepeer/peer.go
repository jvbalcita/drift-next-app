// Package livepeer is the one pion/webrtc peer the Part B rig serves: a single
// H.264 track that carries a live access unit the moment it arrives off the
// device's socket, with no re-encode and no artificial pacing.
//
// Part A's rig paced a file; this one must not add a queue of its own, because
// the number under measurement is the device's own pipeline plus this hop. A
// sample is written as soon as it is read, and the caller records both instants.
package livepeer

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	"spikelocal/arc144/internal/annexb"
)

// PayloadMTU is the outbound MTU pion's TrackLocalStaticSample uses, so the
// packet accounting here matches what actually goes on the wire.
const PayloadMTU = 1200

// Peer is one browser that receives the H.264 track.
type Peer struct {
	pc      *webrtc.PeerConnection
	track   *webrtc.TrackLocalStaticSample
	plid    string
	counter *rtpCounter
	sps     []byte
	pps     []byte

	connected   atomic.Bool
	sender      *webrtc.RTPSender
	mu          sync.Mutex
	writeNS     []int64
	paramsAdded int
}

// New builds a peer connection that offers exactly one H.264 codec, using the
// profile-level-id read out of the device's own SPS.
func New(plid string) (*Peer, error) { return newPeer(plid, true) }

// NewBare builds the same peer with only whatever interceptors the default API
// installs -- the configuration Part A measured -- so the two can be compared on
// one stream when the browser receives less than the hop sent.
func NewBare(plid string) (*Peer, error) { return newPeer(plid, false) }

func newPeer(plid string, explicitRegistry bool) (*Peer, error) {
	if plid == "" {
		plid = "42e01f"
	}
	m := &webrtc.MediaEngine{}
	fmtp := fmt.Sprintf("level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=%s", plid)
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeH264,
			ClockRate:   90000,
			SDPFmtpLine: fmtp,
		},
		PayloadType: 102,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	se := webrtc.SettingEngine{}
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	se.SetIncludeLoopbackCandidate(true)

	// The default interceptors are kept, and one more is added that counts the
	// RTP packets as they leave this hop.
	counter := &rtpCounter{}
	var api *webrtc.API
	if explicitRegistry {
		registry := &interceptor.Registry{}
		if err := webrtc.RegisterDefaultInterceptors(m, registry); err != nil {
			return nil, err
		}
		registry.Add(&factory{counter: counter})
		api = webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithSettingEngine(se),
			webrtc.WithInterceptorRegistry(registry))
	} else {
		api = webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithSettingEngine(se))
	}
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeH264,
		ClockRate:   90000,
		SDPFmtpLine: fmtp,
	}, "video", "drift-arc144-live")
	if err != nil {
		return nil, err
	}
	sender, err := pc.AddTrack(track)
	if err != nil {
		return nil, err
	}
	p := &Peer{pc: pc, track: track, plid: plid, sender: sender, counter: counter}
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		log.Printf("livepeer: connection state %s", st)
		switch st {
		case webrtc.PeerConnectionStateConnected:
			p.connected.Store(true)
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed,
			webrtc.PeerConnectionStateDisconnected:
			p.connected.Store(false)
		}
	})
	// Drain RTCP: an unread queue stalls the interceptor.
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
		}
	}()
	return p, nil
}

// Answer completes the handshake with the browser's offer.
func (p *Peer) Answer(offerSDP string) (string, error) {
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP}); err != nil {
		return "", err
	}
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return "", err
	}
	gathered := webrtc.GatheringCompletePromise(p.pc)
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return "", err
	}
	select {
	case <-gathered:
	case <-time.After(3 * time.Second):
		log.Printf("livepeer: ICE gathering did not complete in 3s; answering with what is gathered")
	}
	local := p.pc.LocalDescription()
	if local == nil {
		return "", fmt.Errorf("no local description")
	}
	return local.SDP, nil
}

// Sample is one access unit handed to the RTP stack.
type Sample struct {
	RecvNS      int64
	StartNS     int64
	EndNS       int64
	Bytes       int
	RTPPackets  int
	RTPBytes    int
	DurationUS  int64
	ParamsAdded bool
	Err         string
}

// nalSpan is one NAL unit's extent in an Annex-B buffer, start code included.
type nalSpan struct {
	off, end, typ int
}

// nalSpans finds the NAL units of an Annex-B buffer.
func nalSpans(b []byte) []nalSpan {
	var out []nalSpan
	i := 0
	for i+3 <= len(b) {
		scLen := 0
		if b[i] == 0 && b[i+1] == 0 {
			switch {
			case b[i+2] == 1:
				scLen = 3
			case i+4 <= len(b) && b[i+2] == 0 && b[i+3] == 1:
				scLen = 4
			}
		}
		if scLen == 0 {
			i++
			continue
		}
		hdr := i + scLen
		if hdr >= len(b) {
			break
		}
		end := len(b)
		for j := hdr + 1; j+3 <= len(b); j++ {
			if b[j] == 0 && b[j+1] == 0 && (b[j+2] == 1 || (j+4 <= len(b) && b[j+2] == 0 && b[j+3] == 1)) {
				end = j
				break
			}
		}
		out = append(out, nalSpan{off: i, end: end, typ: int(b[hdr] & 0x1f)})
		i = end
	}
	return out
}

// attachParameterSets prepends the last SPS/PPS this hop has seen to every IDR
// access unit that does not carry them itself, and reports whether it did.
//
// This is not belt-and-braces. Measured on this fleet: the device's screen
// encoder emits its codec-config packet (SPS/PPS) exactly once, at session
// start, and its IDRs afterwards carry only the slice. A receiver that missed
// that first access unit -- because it attached a moment late, or because the
// very first frames were dropped while its pipeline started -- then never
// decodes anything for the rest of the session, even once a later IDR arrives,
// because it has no parameter sets to decode it with. Every viewer of a live
// device hits this, so the hop has to hold the parameter sets and re-attach
// them; a real implementation also caches the last IDR and sends it to a newly
// attached viewer instead of making it wait for the next one.
func (p *Peer) attachParameterSets(data []byte) ([]byte, bool) {
	spans := nalSpans(data)
	if len(spans) == 0 {
		return data, false
	}
	var sps, pps []byte
	hasIDR, hasParams := false, false
	for _, s := range spans {
		switch s.typ {
		case 7:
			sps = data[s.off:s.end]
			hasParams = true
		case 8:
			pps = data[s.off:s.end]
			hasParams = true
		case 5:
			hasIDR = true
		}
	}

	p.mu.Lock()
	if len(sps) > 0 {
		p.sps = append([]byte(nil), sps...)
	}
	if len(pps) > 0 {
		p.pps = append([]byte(nil), pps...)
	}
	cachedSPS, cachedPPS := p.sps, p.pps
	p.mu.Unlock()

	if hasParams || !hasIDR || len(cachedSPS) == 0 || len(cachedPPS) == 0 {
		return data, false
	}
	out := make([]byte, 0, len(cachedSPS)+len(cachedPPS)+len(data))
	out = append(out, cachedSPS...)
	out = append(out, cachedPPS...)
	out = append(out, data...)
	return out, true
}

// Write forwards one access unit as a single sample and reports how long the Go
// hop held it. The duration is the encoder's own PTS delta wherever the caller
// has one, so the RTP timestamps follow the device's cadence instead of an
// assumed frame rate.
func (p *Peer) Write(data []byte, duration time.Duration) Sample {
	s := Sample{RecvNS: time.Now().UnixNano()}
	data, s.ParamsAdded = p.attachParameterSets(data)
	payloader := &codecs.H264Payloader{}
	for _, pl := range payloader.Payload(PayloadMTU, data) {
		s.RTPPackets++
		s.RTPBytes += len(pl)
	}
	s.Bytes = len(data)
	s.StartNS = time.Now().UnixNano()
	if err := p.track.WriteSample(media.Sample{Data: data, Duration: duration}); err != nil {
		s.Err = err.Error()
	}
	s.EndNS = time.Now().UnixNano()
	p.mu.Lock()
	p.writeNS = append(p.writeNS, s.StartNS)
	p.mu.Unlock()
	return s
}

// Connected reports whether the browser's peer connection is up.
func (p *Peer) Connected() bool { return p.connected.Load() }

// ProfileLevelID reads the profile-level-id out of the first SPS in an Annex-B
// buffer: the device's hardware encoder chooses the profile, so the SDP must
// state the stream's own values rather than a constant.
func ProfileLevelID(b []byte) (string, error) {
	nals, err := annexb.NALs(b)
	if err != nil {
		return "", err
	}
	for _, n := range nals {
		if n.Type != 7 {
			continue
		}
		if len(n.Payload) < 3 {
			return "", fmt.Errorf("SPS too short (%d bytes)", len(n.Payload))
		}
		return fmt.Sprintf("%02x%02x%02x", n.Payload[0], n.Payload[1], n.Payload[2]), nil
	}
	return "", fmt.Errorf("no SPS in %d bytes", len(b))
}

// Counts reports what this hop has emitted on the wire.
func (p *Peer) Counts() Counts {
	if p.counter == nil {
		return Counts{}
	}
	return p.counter.snapshot()
}

// Close releases the peer connection.
func (p *Peer) Close() {
	if p.pc != nil {
		_ = p.pc.Close()
	}
}
