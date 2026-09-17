package main

import (
	"log"
	"time"

	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4/pkg/media"
)

// frameSend is one access unit's send record: when it was due, when the Go hop
// handed it to the RTP stack, and how it was packetised.
type frameSend struct {
	Frame        int   `json:"frame"`
	SchedNS      int64 `json:"sched_ns"`
	SendStartNS  int64 `json:"send_start_ns"`
	SendEndNS    int64 `json:"send_end_ns"`
	LateNS       int64 `json:"late_ns"`
	Bytes        int   `json:"bytes"`
	RTPPackets   int   `json:"rtp_packets"`
	RTPBytes     int   `json:"rtp_bytes"`
	NoSubscriber bool  `json:"no_subscriber"`
}

// fragSend is one fragmented-MP4 fragment's delivery record.
type fragSend struct {
	Index      int   `json:"index"`
	StartFrame int   `json:"start_frame"`
	EndFrame   int   `json:"end_frame"`
	SchedNS    int64 `json:"sched_ns"`
	PushNS     int64 `json:"push_ns"`
	FlushNS    int64 `json:"flush_ns"`
	Bytes      int   `json:"bytes"`
	Dropped    bool  `json:"dropped"`
}

type runLog struct {
	Label         string      `json:"label"`
	FPS           int         `json:"fps"`
	FrameCount    int         `json:"frame_count"`
	Source        string      `json:"source"`
	MP4           string      `json:"mp4"`
	Fragments     int         `json:"fragments"`
	Timescale     uint32      `json:"timescale"`
	ProfileLID    string      `json:"profile_level_id"`
	StartNS       int64       `json:"start_ns"`
	EndNS         int64       `json:"end_ns"`
	Notes         []string    `json:"notes"`
	Frames        []frameSend `json:"frames"`
	FragmentSends []fragSend  `json:"fragment_sends"`
}

// pace is the single real-time source both transports are driven from: frame i
// is released at start + i/fps, exactly once, to the RTP track and to the MSE
// consumer. Because one clock drives both, the two transports are compared on
// the same bytes at the same instants.
func (s *server) pace() {
	period := time.Second / time.Duration(s.cfg.fps)
	payloader := &codecs.H264Payloader{}

	s.mu.Lock()
	start := time.Now()
	s.startNS = start.UnixNano()
	s.started = true
	rtp := s.rtp
	mse := s.mse
	s.mu.Unlock()

	s.logMu.Lock()
	s.log.StartNS = start.UnixNano()
	s.logMu.Unlock()

	log.Printf("spike: pacing %d frames at %d fps from %s", len(s.aus), s.cfg.fps, start.Format(time.RFC3339Nano))

	fragIdx := 0
	sends := make([]frameSend, 0, len(s.aus))
	for i, au := range s.aus {
		sched := start.Add(time.Duration(i) * period)
		if d := time.Until(sched); d > 0 {
			time.Sleep(d)
		}
		sendStart := time.Now()
		rec := frameSend{
			Frame:       i,
			SchedNS:     sched.UnixNano(),
			SendStartNS: sendStart.UnixNano(),
			LateNS:      sendStart.Sub(sched).Nanoseconds(),
			Bytes:       len(au.Data),
		}
		// Packetisation accounting with the same MTU and payloader pion itself
		// uses (outboundMTU = 1200 for TrackLocalStaticSample), so the report
		// can state what the RTP hop actually put on the wire.
		payloads := payloader.Payload(1200, au.Data)
		rec.RTPPackets = len(payloads)
		for _, p := range payloads {
			rec.RTPBytes += len(p)
		}
		if rtp == nil {
			rec.NoSubscriber = true
		} else if err := rtp.track.WriteSample(media.Sample{Data: au.Data, Duration: period}); err != nil {
			rec.NoSubscriber = true
		}
		rec.SendEndNS = time.Now().UnixNano()
		sends = append(sends, rec)

		for fragIdx < len(s.frags) && s.frags[fragIdx].StartFrame <= i {
			fr := s.frags[fragIdx]
			fragIdx++
			pushNS := time.Now().UnixNano()
			if mse != nil {
				mse.push(fr, pushNS)
			}
		}
	}
	if mse != nil {
		mse.close()
	}

	s.logMu.Lock()
	s.log.Frames = sends
	s.log.EndNS = time.Now().UnixNano()
	if mse != nil {
		s.log.FragmentSends = mse.records()
	}
	s.logMu.Unlock()

	s.mu.Lock()
	s.finished = true
	s.mu.Unlock()
	log.Printf("spike: pacing finished")
}
