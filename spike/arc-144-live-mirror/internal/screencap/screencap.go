// Package screencap pulls a PNG of the device's current screen and reduces it to
// the single luma channel the clock strip is read in.
//
// `exec-out` rather than `shell`: `adb shell` rewrites line endings and would
// corrupt the PNG.
package screencap

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"

	"spikelocal/arc144/internal/clockcode"
)

// Take returns a path to one PNG of the device's current screen. The caller
// owns the file and should remove it.
func Take(adb, serial, path string) (string, error) {
	cmd := exec.Command(adb, "-s", serial, "exec-out", "screencap", "-p")
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("screencap: %v: %s", err, errBuf.String())
	}
	if out.Len() < 1024 {
		return "", fmt.Errorf("screencap returned %d bytes: %s", out.Len(), errBuf.String())
	}
	if path == "" {
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
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// LoadGray decodes a PNG into luma.
func LoadGray(path string) (clockcode.Gray, error) {
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
	if src, ok := img.(*image.Gray); ok && src.Stride == src.Rect.Dx() {
		copy(g.Pix, src.Pix)
		return g, nil
	}
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, gg, bb, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			// Rec. 601 luma, close enough for a pure black/white strip to the
			// same values the browser's canvas readback reports.
			g.Pix[y*g.Width+x] = uint8((299*(r>>8) + 587*(gg>>8) + 114*(bb>>8)) / 1000)
		}
	}
	return g, nil
}
