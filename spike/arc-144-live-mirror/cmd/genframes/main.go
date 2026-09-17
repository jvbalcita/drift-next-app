// Command genframes writes a synthetic H.264 Annex-B elementary stream to a
// file, via the ffmpeg on this machine.
//
// It is the "camera" of this spike: a deterministic 1280x720 yuv420p frame
// source in which every frame carries its own index in a barcode (see
// internal/barcode), piped into libx264. The stream is generated, never
// committed: the repository keeps the generator, not the media.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"

	"spikelocal/arc144/internal/barcode"
)

func main() {
	var (
		out    = flag.String("out", "out.h264", "output Annex-B path")
		frames = flag.Int("frames", 600, "number of frames")
		w      = flag.Int("w", 1280, "width")
		h      = flag.Int("h", 720, "height")
		fps    = flag.Int("fps", 30, "frames per second")
		crf    = flag.Int("crf", 20, "x264 crf")
	)
	flag.Parse()

	if err := run(*out, *frames, *w, *h, *fps, *crf); err != nil {
		log.Fatalf("genframes: %v", err)
	}
}

func run(out string, frames, w, h, fps, crf int) error {
	args := []string{
		"-hide_banner", "-nostdin", "-y", "-loglevel", "error",
		"-f", "rawvideo", "-pix_fmt", "yuv420p",
		"-s", fmt.Sprintf("%dx%d", w, h), "-r", strconv.Itoa(fps),
		"-i", "pipe:0",
		"-an", "-c:v", "libx264",
		"-preset", "veryfast",
		"-profile:v", "baseline", "-level", "3.1",
		"-crf", strconv.Itoa(crf),
		"-bf", "0",
		// One slice per picture, keyframe every second, SPS/PPS repeated on
		// every keyframe so a consumer that joins late (or starts a MediaSource
		// at a fragment boundary) can initialise without the stream head.
		"-x264-params", fmt.Sprintf(
			"sliced-threads=0:threads=1:bframes=0:rc-lookahead=0:sync-lookahead=0:keyint=%d:min-keyint=%d:scenecut=0:repeat-headers=1",
			fps, fps),
		"-f", "h264", out,
	}
	cmd := exec.Command("ffmpeg", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	enc := newPainter(w, h)
	frame := make([]byte, enc.frameSize())
	werr := error(nil)
	for i := 0; i < frames; i++ {
		enc.paint(frame, i)
		if _, err := stdin.Write(frame); err != nil {
			werr = err
			break
		}
	}
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg: %v: %s", err, stderr.String())
	}
	if werr != nil {
		return werr
	}
	st, err := os.Stat(out)
	if err != nil {
		return err
	}
	log.Printf("genframes: wrote %s (%d bytes, %d frames, %dx%d@%d, crf %d)",
		out, st.Size(), frames, w, h, fps, crf)
	return nil
}

// painter draws the synthetic scene.
type painter struct {
	w, h   int
	stride int
	ySize  int
	cSize  int
	uw, uh int
	grad   []byte
}

func newPainter(w, h int) *painter {
	p := &painter{
		w: w, h: h, stride: w,
		ySize: w * h,
		cSize: (w / 2) * (h / 2),
		uw:    w / 2, uh: h / 2,
	}
	p.grad = make([]byte, w)
	for x := range p.grad {
		p.grad[x] = byte(40 + (x*60)/w)
	}
	return p
}

// frameSize is the yuv420p size of one frame.
func (p *painter) frameSize() int { return p.ySize + 2*p.cSize }

// paint fills frame (a yuv420p buffer of exactly frameSize bytes) with the
// synthetic scene carrying the given barcode index.
func (p *painter) paint(frame []byte, index int) {
	y := frame[:p.ySize]
	u := frame[p.ySize : p.ySize+p.cSize]
	v := frame[p.ySize+p.cSize:]

	// Static horizontal gradient background (precomputed once) plus a moving
	// bright bar, so consecutive frames differ everywhere and a consumer
	// cannot pass off a cached frame as a fresh one.
	for yy := 0; yy < p.h; yy++ {
		copy(y[yy*p.stride:yy*p.stride+p.stride], p.grad)
	}
	barX := (index * 8) % p.w
	for xx := barX; xx < barX+80 && xx < p.w; xx++ {
		for yy := 0; yy < p.h; yy++ {
			y[yy*p.stride+xx] = 200
		}
	}
	// A moving bright square: obvious motion for a human watching the page.
	sqX := (index * 20) % (p.w - 120)
	sqY := 320 + (index*7)%200 - 100
	for yy := sqY; yy < sqY+120; yy++ {
		if yy < 0 || yy >= p.h {
			continue
		}
		row := y[yy*p.stride : yy*p.stride+p.stride]
		for xx := sqX; xx < sqX+120; xx++ {
			row[xx] = 235
		}
	}
	barcode.DrawY(y, p.stride, p.w, p.h, index)

	for i := range u {
		u[i] = 128
	}
	for i := range v {
		v[i] = 128
	}
}
