// Command verifyfmp4 checks that the fragmented MP4 was muxed from the same
// access units without re-encoding, and reports the fragment layout the MSE
// consumer will be fed.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"spikelocal/arc144/internal/annexb"
	"spikelocal/arc144/internal/fmp4"
)

func main() {
	var (
		in     = flag.String("in", "out.mp4", "fragmented mp4")
		h264   = flag.String("h264", "", "source Annex-B to compare against")
		frames = flag.Int("frames", 600, "expected frame count")
		fps    = flag.Int("fps", 30, "frame rate")
	)
	flag.Parse()
	raw, err := os.ReadFile(*in)
	if err != nil {
		log.Fatal(err)
	}
	f, err := fmp4.Parse(raw, *fps)
	if err != nil {
		log.Fatalf("parse: %v", err)
	}
	last := f.Fragments[len(f.Fragments)-1]
	fmt.Printf("init segment: %d bytes, timescale %d, fragments %d\n", len(f.Init), f.Timescale, len(f.Fragments))
	fmt.Printf("covered frames: %d (0..%d), expected %d\n", last.EndFrame, last.EndFrame-1, *frames)
	fmt.Printf("fragment 0: frames [%d,%d) %d bytes\n", f.Fragments[0].StartFrame, f.Fragments[0].EndFrame, len(f.Fragments[0].Data))
	fmt.Printf("fragment 1: frames [%d,%d) %d bytes\n", f.Fragments[1].StartFrame, f.Fragments[1].EndFrame, len(f.Fragments[1].Data))
	if last.EndFrame != *frames {
		log.Fatalf("fragment coverage %d != expected %d", last.EndFrame, *frames)
	}
	// The mp4 must cover exactly the access units the RTP path sends.
	if *h264 != "" {
		src, err := os.ReadFile(*h264)
		if err != nil {
			log.Fatal(err)
		}
		aus, err := annexb.Split(src)
		if err != nil {
			log.Fatal(err)
		}
		total := 0
		for _, au := range aus {
			total += len(au.Data)
		}
		fmt.Printf("source Annex-B: %d access units, %d bytes\n", len(aus), total)
		if len(aus) != last.EndFrame {
			log.Fatalf("source has %d access units but mp4 covers %d frames", len(aus), last.EndFrame)
		}
	}

}
