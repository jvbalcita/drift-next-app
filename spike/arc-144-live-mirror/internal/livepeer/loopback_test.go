package livepeer

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"

	"spikelocal/arc144/internal/annexb"
)

// TestLoopbackCarriesEveryAccessUnit sends a captured device stream through this
// package's hop to a second pion peer in the same process and counts what the
// receiver got.
//
// It exists because the browser's counters said two contradictory things -- one
// run received every frame and decoded none, another received a third of the
// packets -- and neither of those numbers can say whether the loss was the
// transport, the depacketiser, or the browser's rendering pipeline. This test has
// no browser in it: it answers the transport and depacketiser questions
// directly, and it stays as the regression check for the hop.
//
// Set ARC144_CAPTURE to a captured Annex-B stream to run it.
func TestLoopbackCarriesEveryAccessUnit(t *testing.T) {
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
	if len(aus) > 300 {
		aus = aus[:300]
	}

	sender, err := New("420032")
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()

	receiver, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()

	var mu sync.Mutex
	packets, markers, assembled, bytes := 0, 0, 0, 0
	firstErr := error(nil)
	depack := &codecs.H264Packet{}
	// One RTP timestamp per access unit: that is the property the payloader and
	// the sample duration are responsible for, and it is counted directly rather
	// than inferred, because a completed "unit" from the depacketiser also
	// includes the parameter-set packet that now precedes every IDR.
	stamps := map[uint32]struct{}{}

	receiver.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			pkt, _, err := track.ReadRTP()
			if err != nil {
				return
			}
			mu.Lock()
			packets++
			stamps[pkt.Timestamp] = struct{}{}
			bytes += len(pkt.Payload)
			if pkt.Marker {
				markers++
			}
			// Unmarshal returns a completed access unit on the packet that ends
			// one, which is the same boundary the browser's depacketiser uses.
			if nalu, err := depack.Unmarshal(pkt.Payload); err == nil && len(nalu) > 0 {
				assembled++
			} else if err != nil && firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
		}
	})
	if _, err := receiver.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		t.Fatal(err)
	}

	offer, err := receiver.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := receiver.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	answerSDP, err := sender.Answer(offer.SDP)
	if err != nil {
		t.Fatal(err)
	}
	if err := receiver.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answerSDP}); err != nil {
		t.Fatal(err)
	}

	connected := make(chan struct{})
	receiver.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		if st == webrtc.PeerConnectionStateConnected {
			select {
			case <-connected:
			default:
				close(connected)
			}
		}
	})
	select {
	case <-connected:
	case <-time.After(10 * time.Second):
		t.Fatal("the loopback peer never connected")
	}
	// Let the receiver's track be established before the first frame: an IDR
	// sent before anything is listening is not recoverable on this stream.
	time.Sleep(1500 * time.Millisecond)

	start := time.Now()
	for i, au := range aus {
		if d := time.Until(start.Add(time.Duration(i) * time.Second / 60)); d > 0 {
			time.Sleep(d)
		}
		if s := sender.Write(au.Data, time.Second/60); s.Err != "" {
			t.Fatalf("writing access unit %d: %s", i, s.Err)
		}
	}
	time.Sleep(1500 * time.Millisecond)

	counts := sender.Counts()
	mu.Lock()
	defer mu.Unlock()
	t.Logf("sender: %d packets, %d bytes, %d markers", counts.Packets, counts.Bytes, counts.Markers)
	t.Logf("receiver: %d packets, %d bytes, %d markers, %d RTP timestamps, %d depacketiser units (first error: %v)",
		packets, bytes, markers, len(stamps), assembled, firstErr)
	if packets != counts.Packets {
		t.Errorf("the receiver got %d of %d packets: %d did not cross the hop",
			packets, counts.Packets, counts.Packets-packets)
	}
	if len(stamps) != len(aus) {
		t.Errorf("the receiver saw %d RTP timestamps for %d access units sent", len(stamps), len(aus))
	}
}
