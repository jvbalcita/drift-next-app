// Command verifyframes validates the measurement instrument.
//
// The spike's latency numbers come from a barcode read by the browser out of
// decoded pixels. That read is only trustworthy if the mark survives the whole
// chain: raw frame -> x264 encode -> Annex-B file -> ffmpeg decode -> raw
// frame. This command runs that chain and asserts every decoded frame decodes
// back to its own index, and that the access-unit splitter agrees with the
// frame count ffmpeg reports.
//
// If this fails, the browser-side numbers are meaningless, so it runs before
// any measurement is taken.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"spikelocal/arc144/internal/annexb"
	"spikelocal/arc144/internal/barcode"
)

func main() {
	var (
		in     = flag.String("in", "out.h264", "Annex-B input")
		frames = flag.Int("frames", 600, "expected frame count")
		w      = flag.Int("w", 1280, "width")
		h      = flag.Int("h", 720, "height")
		fps    = flag.Int("fps", 30, "frame rate, for the decode rate")
	)
	flag.Parse()
	if err := run(*in, *frames, *w, *h, *fps); err != nil {
		log.Fatalf("verifyframes: %v", err)
	}
}

func run(in string, frames, w, h, fps int) error {
	raw, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	aus, err := annexb.Split(raw)
	if err != nil {
		return fmt.Errorf("splitting access units: %w", err)
	}
	vcl, err := annexb.CountVCL(raw)
	if err != nil {
		return err
	}
	fmt.Printf("access units: %d (expected %d)\n", len(aus), frames)
	fmt.Printf("VCL NAL units: %d (one slice per picture -> %d)\n", vcl, vcl)
	if len(aus) != frames {
		return fmt.Errorf("access unit count %d != expected %d", len(aus), frames)
	}
	if vcl != frames {
		return fmt.Errorf("VCL NAL count %d != expected %d (multi-slice source?)", vcl, frames)
	}

	decoded, err := decodeRaw(in, w, h, fps)
	if err != nil {
		return err
	}
	if len(decoded) != frames {
		return fmt.Errorf("ffmpeg decoded %d frames, expected %d", len(decoded), frames)
	}

	ySize := w * h
	bad := 0
	for i, frame := range decoded {
		idx, ok := barcode.DecodeY(frame, w, w, h)
		if !ok || idx != i {
			bad++
			if bad <= 5 {
				fmt.Printf("frame %d: decoded index=%d ok=%v\n", i, idx, ok)
			}
		}
	}
	if bad != 0 {
		return fmt.Errorf("%d/%d decoded frames did not read back as their own index", bad, frames)
	}
	fmt.Printf("barcode round-trip: %d/%d frames decoded to their own index (survives x264 + decode)\n", frames, frames)
	_ = ySize
	return nil
}

// decodeRaw decodes the Annex-B file to raw yuv420p frames with ffmpeg.
func decodeRaw(in string, w, h, fps int) ([][]byte, error) {
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-nostdin", "-loglevel", "error",
		"-f", "h264", "-r", strconv.Itoa(fps), "-i", in,
		"-f", "rawvideo", "-pix_fmt", "yuv420p", "pipe:1",
	)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg decode: %v: %s", err, errb.String())
	}
	frameSize := w * h * 3 / 2
	if out.Len()%frameSize != 0 {
		return nil, fmt.Errorf("decoded %d bytes, not a multiple of frame size %d: %s",
			out.Len(), frameSize, strings.TrimSpace(errb.String()))
	}
	b := out.Bytes()
	n := out.Len() / frameSize
	frames := make([][]byte, n)
	for i := 0; i < n; i++ {
		frames[i] = b[i*frameSize : (i+1)*frameSize]
	}
	return frames, nil
}
