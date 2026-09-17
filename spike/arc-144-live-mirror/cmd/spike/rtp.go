package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
)

// rtpClient is the one browser peer that receives the H.264 track over RTP.
type rtpClient struct {
	pc        *webrtc.PeerConnection
	track     *webrtc.TrackLocalStaticSample
	connected atomic.Bool
}

func (s *server) handleOffer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SDP string `json:"sdp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SDP == "" {
		http.Error(w, "bad offer", http.StatusBadRequest)
		return
	}

	client, err := s.newRTPClient()
	if err != nil {
		log.Printf("spike: peer connection: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: body.SDP}
	if err := client.pc.SetRemoteDescription(offer); err != nil {
		log.Printf("spike: set remote description: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	answer, err := client.pc.CreateAnswer(nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	gathered := webrtc.GatheringCompletePromise(client.pc)
	if err := client.pc.SetLocalDescription(answer); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	select {
	case <-gathered:
	case <-time.After(3 * time.Second):
		log.Printf("spike: ICE gathering did not complete in 3s; answering with what is gathered")
	}

	local := client.pc.LocalDescription()
	if local == nil {
		http.Error(w, "no local description", http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	if s.rtp != nil {
		_ = s.rtp.pc.Close()
	}
	s.rtp = client
	s.mu.Unlock()

	log.Printf("spike: answered offer, %d stats-gathering candidates in SDP", countCandidates(local.SDP))
	writeJSON(w, map[string]any{"sdp": local.SDP})
}

// newRTPClient builds a peer connection that offers exactly one H.264 video
// codec, so the negotiated codec cannot be anything else.
func (s *server) newRTPClient() (*rtpClient, error) {
	m := &webrtc.MediaEngine{}
	fmtp := fmt.Sprintf("level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=%s", s.plid)
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
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithSettingEngine(se))

	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeH264,
		ClockRate:   90000,
		SDPFmtpLine: fmtp,
	}, "video", "drift-spike")
	if err != nil {
		return nil, err
	}
	sender, err := pc.AddTrack(track)
	if err != nil {
		return nil, err
	}
	// Drain RTCP so the interceptor does not stall on an unread queue.
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
		}
	}()

	client := &rtpClient{pc: pc, track: track}
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		log.Printf("spike: rtp peer connection state %s", st)
		switch st {
		case webrtc.PeerConnectionStateConnected:
			client.connected.Store(true)
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed,
			webrtc.PeerConnectionStateDisconnected:
			client.connected.Store(false)
		}
	})
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		log.Printf("spike: unexpected inbound track %s", tr.Codec().MimeType)
	})
	return client, nil
}

func countCandidates(sdp string) int {
	n := 0
	for _, line := range splitLines(sdp) {
		if len(line) > 12 && line[:12] == "a=candidate:" {
			n++
		}
	}
	return n
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
