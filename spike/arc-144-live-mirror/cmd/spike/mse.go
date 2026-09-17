package main

import (
	"encoding/binary"
	"log"
	"net/http"
	"sync"
	"time"

	"spikelocal/arc144/internal/fmp4"
)

// mseSink streams fragments of the fragmented MP4 to one browser consumer.
//
// Fragments are handed to a goroutine rather than written inline from the
// pacer: the pacer must stay on schedule so both transports see the same frame
// cadence. The sink records both the hand-off time and the time the bytes were
// flushed, so a slow consumer shows up as transport latency instead of
// silently stalling the source. A full queue is recorded as a drop rather than
// blocking, and a drop breaks the byte stream, so any drop is reported.
type mseSink struct {
	w http.ResponseWriter
	f http.Flusher

	ch   chan fmp4.Fragment
	done chan struct{}

	mu     sync.Mutex
	order  []int
	recs   map[int]*fragSend
	closed bool
}

func newMSESink(w http.ResponseWriter, f http.Flusher) *mseSink {
	s := &mseSink{
		w: w, f: f,
		ch:   make(chan fmp4.Fragment, 600),
		done: make(chan struct{}),
		recs: map[int]*fragSend{},
	}
	go s.run()
	return s
}

func (s *mseSink) run() {
	defer close(s.done)
	for fr := range s.ch {
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(fr.Data)))
		if _, err := s.w.Write(hdr[:]); err != nil {
			log.Printf("spike: mse write header: %v", err)
			return
		}
		if _, err := s.w.Write(fr.Data); err != nil {
			log.Printf("spike: mse write body: %v", err)
			return
		}
		s.f.Flush()
		s.markFlushed(fr.Index, time.Now().UnixNano())
	}
}

func (s *mseSink) push(fr fmp4.Fragment, pushNS int64) {
	s.mu.Lock()
	rec := &fragSend{
		Index:      fr.Index,
		StartFrame: fr.StartFrame,
		EndFrame:   fr.EndFrame,
		PushNS:     pushNS,
		Bytes:      len(fr.Data),
	}
	s.recs[fr.Index] = rec
	s.order = append(s.order, fr.Index)
	s.mu.Unlock()

	select {
	case s.ch <- fr:
	default:
		s.mu.Lock()
		rec.Dropped = true
		s.mu.Unlock()
	}
}

func (s *mseSink) markFlushed(index int, ns int64) {
	s.mu.Lock()
	if rec, ok := s.recs[index]; ok {
		rec.FlushNS = ns
	}
	s.mu.Unlock()
}

func (s *mseSink) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	close(s.ch)
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		log.Printf("spike: mse sink did not drain within 10s")
	}
}

func (s *mseSink) records() []fragSend {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]fragSend, 0, len(s.order))
	for _, i := range s.order {
		out = append(out, *s.recs[i])
	}
	return out
}

func (s *server) handleMSEInit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", itoa(len(s.init)))
	if _, err := w.Write(s.init); err != nil {
		log.Printf("spike: writing init segment: %v", err)
	}
}

// handleMSEStream delivers each fragment as the pacer releases it, framed as
// [uint32 length][moof+mdat]. The init segment is served separately by
// /mse/init; nothing else goes on this stream, so the consumer's framing can
// never desynchronise.
func (s *server) handleMSEStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	sink := newMSESink(w, flusher)
	s.mu.Lock()
	if s.mse != nil {
		s.mse.close()
	}
	s.mse = sink
	s.mu.Unlock()
	log.Printf("spike: mse consumer attached from %s", r.RemoteAddr)

	<-sink.done
	log.Printf("spike: mse consumer stream ended")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
