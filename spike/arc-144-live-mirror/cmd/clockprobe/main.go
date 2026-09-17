// Command clockprobe checks the device-side half of the glass-to-glass
// measurement without any video path: it asks the device for a screenshot,
// finds the clock strip the device page painted into its own pixels, and
// decodes the millisecond clock out of it.
//
// That is the instrument's own validation. If the strip cannot be found in a
// screenshot, the strip in the video is not trustworthy either, and the run
// reports a gap rather than a number.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"os/exec"
	"time"

	"spikelocal/arc144/internal/clockcode"
)

func main() {
	var serial, pngPath, out string
	var adb string
	var samples int
	flag.StringVar(&serial, "serial", "", "device serial for adb exec-out screencap")
	flag.StringVar(&adb, "adb", "adb", "adb binary")
	flag.StringVar(&pngPath, "png", "", "decode this PNG file instead of taking a screenshot")
	flag.StringVar(&out, "out", "", "write the JSON report here")
	flag.IntVar(&samples, "samples", 1, "how many screenshots to take and decode")
	flag.Parse()

	type sample struct {
		At          string             `json:"at"`
		HostMS      int64              `json:"host_ms_before"`
		HostMSAfter int64              `json:"host_ms_after"`
		Location    clockcode.Location `json:"location"`
		ElapsedMS   int64              `json:"screencap_ms"`
		Error       string             `json:"error,omitempty"`
	}
	report := struct {
		Serial  string   `json:"serial"`
		PNG     string   `json:"png,omitempty"`
		Samples []sample `json:"samples"`
		Strip   struct {
			File  clockcode.Location `json:"file"`
			Stats clockcode.Stats    `json:"stats"`
		} `json:"strip"`
	}{Serial: serial, PNG: pngPath}

	source := pngPath
	if source == "" && serial == "" {
		log.Fatal("clockprobe: -serial or -png is required")
	}

	var ms []int64
	for i := 0; i < samples; i++ {
		before := time.Now().UnixNano()
		path := source
		var elapsed time.Duration
		if path == "" {
			var err error
			path, err = screencap(adb, serial)
			if err != nil {
				log.Fatalf("clockprobe: screencap: %v", err)
			}
			elapsed = time.Since(time.Unix(0, before))
			defer os.Remove(path)
		}
		after := time.Now().UnixNano()
		s := sample{At: time.Now().Format(time.RFC3339Nano), HostMS: before / 1e6, HostMSAfter: after / 1e6, ElapsedMS: elapsed.Milliseconds()}
		g, err := loadGray(path)
		if err != nil {
			s.Error = err.Error()
			report.Samples = append(report.Samples, s)
			continue
		}
		loc, err := clockcode.Locate(g, after/1e6, 5000)
		if err != nil {
			// Fall back to the tolerant locator purely to report where the strip
			// is (or is not), never to take a measurement from.
			approx, aerr := clockcode.LocateTolerant(g, after/1e6, 5000, 64, 700)
			if aerr == nil {
				s.Error = fmt.Sprintf("%v; tolerant locator says (%d,%d) method=%s", err, approx.X, approx.Y, approx.Method)
			} else {
				s.Error = err.Error()
			}
			report.Samples = append(report.Samples, s)
			continue
		}
		s.Location = loc
		report.Samples = append(report.Samples, s)
		ms = append(ms, loc.MS)
		if report.Strip.File.Method == "" {
			report.Strip.File = loc
		}
	}
	report.Strip.Stats = clockcode.Summarise(ms)

	body, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(body))
	if out != "" {
		if err := os.WriteFile(out, body, 0o644); err != nil {
			log.Fatalf("clockprobe: writing %s: %v", out, err)
		}
	}
	if len(ms) == 0 {
		os.Exit(2)
	}
}

// screencap pulls one PNG of the device's current screen. `exec-out` avoids the
// CRLF mangling that `adb shell` applies to binary output.
func screencap(adb, serial string) (string, error) {
	cmd := exec.Command(adb, "-s", serial, "exec-out", "screencap", "-p")
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, errBuf.String())
	}
	if out.Len() < 1024 {
		return "", fmt.Errorf("screencap returned %d bytes: %s", out.Len(), errBuf.String())
	}
	f, err := os.CreateTemp("", "arc144-screencap-*.png")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(out.Bytes()); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func loadGray(path string) (clockcode.Gray, error) {
	f, err := os.Open(path)
	if err != nil {
		return clockcode.Gray{}, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return clockcode.Gray{}, err
	}
	b := img.Bounds()
	g := clockcode.Gray{Pix: make([]uint8, b.Dx()*b.Dy()), Width: b.Dx(), Height: b.Dy()}
	switch src := img.(type) {
	case *image.Gray:
		for y := 0; y < b.Dy(); y++ {
			copy(g.Pix[y*g.Width:(y+1)*g.Width], src.Pix[y*src.Stride:(y*src.Stride)+g.Width])
		}
	default:
		for y := 0; y < b.Dy(); y++ {
			for x := 0; x < b.Dx(); x++ {
				r, gg, bb, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				// Rec. 601 luma, matching what a browser's canvas readback
				// reports for the same pixels closely enough for a
				// black/white strip.
				g.Pix[y*g.Width+x] = uint8((299*(r>>8) + 587*(gg>>8) + 114*(bb>>8)) / 1000)
			}
		}
	}
	return g, nil
}
