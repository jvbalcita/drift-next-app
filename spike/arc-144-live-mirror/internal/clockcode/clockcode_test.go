package clockcode

import (
	"testing"
)

func TestRoundTrip(t *testing.T) {
	cases := []Value{
		{MS: 0},
		{MS: 1789667777187},
		{MS: 1789667777187, Reacting: true},
		{MS: 1789667777187, TapCount: 3},
		{MS: (1 << 48) - 1, Reacting: true, TapCount: 2},
	}
	for _, want := range cases {
		got, err := DecodeBits(want.Bits())
		if err != nil {
			t.Fatalf("DecodeBits(%+v): %v", want, err)
		}
		if got != want {
			t.Fatalf("round trip: got %+v want %+v", got, want)
		}
	}
}

// A single flipped cell must be refused, never decoded into a plausible value:
// the measurement's correctness depends on a bad read being no read.
func TestCorruptionIsRefused(t *testing.T) {
	v := Value{MS: 1789667777187, TapCount: 1}
	bits := v.Bits()
	accepted := 0
	for i := range bits {
		flipped := append([]int(nil), bits...)
		flipped[i] ^= 1
		if got, err := DecodeBits(flipped); err == nil {
			accepted++
			t.Logf("cell %d flipped -> accepted as %+v", i, got)
		}
	}
	// Every cell is covered by the sync pattern or by the parity cell, so no
	// single-cell corruption may decode at all.
	if accepted != 0 {
		t.Fatalf("accepted %d of %d single-cell corruptions, want 0", accepted, len(bits))
	}
}

func paintStrip(g Gray, x0, y0 int, v Value) {
	bits := v.Bits()
	for row := 0; row < CellsY; row++ {
		for col := 0; col < CellsX; col++ {
			val := uint8(0)
			if bits[row*CellsX+col] == 1 {
				val = 255
			}
			for dy := 0; dy < Cell; dy++ {
				for dx := 0; dx < Cell; dx++ {
					g.Pix[(y0+row*Cell+dy)*g.Width+x0+col*Cell+dx] = val
				}
			}
		}
	}
}

func TestLocateFindsStripUnderChromeNoise(t *testing.T) {
	const w, h = 1080, 2280
	g := Gray{Pix: make([]uint8, w*h), Width: w, Height: h}
	// 0..196: a white toolbar full of text-like noise, then a dark page.
	for y := 0; y < 196; y++ {
		for x := 0; x < w; x++ {
			g.Pix[y*w+x] = 250
		}
	}
	for i := 196 * w; i < len(g.Pix); i++ {
		g.Pix[i] = 20
	}
	for y := 0; y < 196; y++ {
		for x := 0; x < w; x += 7 {
			g.Pix[y*w+x] = 30
		}
	}
	v := Value{MS: 1789667777187, Reacting: true, TapCount: 2}
	paintStrip(g, 0, 300, v)

	loc, err := Locate(g, v.MS+20, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if loc.X != 0 || loc.Y != 300 {
		t.Fatalf("located at (%d,%d), want exactly (0,300)", loc.X, loc.Y)
	}
	if loc.MS != v.MS || !loc.React || loc.TapCnt != 2 {
		t.Fatalf("decoded %+v, want %+v", loc, v)
	}
}

// The exact locator must refuse a strip that is not there, rather than
// returning the best of a bad set: a wrong origin silently shifts every bit.
func TestLocateRefusesWithoutStrip(t *testing.T) {
	const w, h = 1080, 2280
	g := Gray{Pix: make([]uint8, w*h), Width: w, Height: h}
	for i := range g.Pix {
		g.Pix[i] = 20
	}
	if _, err := Locate(g, 1789667777187, 2000); err == nil {
		t.Fatal("located a strip in a blank screen")
	}
}

func TestLocateRejectsStaleClock(t *testing.T) {
	const w, h = 1080, 2280
	g := Gray{Pix: make([]uint8, w*h), Width: w, Height: h}
	paintStrip(g, 0, 300, Value{MS: 1789667777187})
	if _, err := Locate(g, 1789667777187+60000, 2000); err == nil {
		t.Fatal("locate accepted a strip whose clock is a minute away from the reference")
	}
}

func TestLocateRejectsScaledStrip(t *testing.T) {
	// A strip painted at half scale (the failure mode when the page's CSS pixel
	// is not a device pixel) is not the strip this codec reads.
	const w, h = 1080, 2280
	g := Gray{Pix: make([]uint8, w*h), Width: w, Height: h}
	v := Value{MS: 1789667777187}
	bits := v.Bits()
	for row := 0; row < CellsY; row++ {
		for col := 0; col < CellsX; col++ {
			val := uint8(20)
			if bits[row*CellsX+col] == 1 {
				val = 250
			}
			for dy := 0; dy < Cell/2; dy++ {
				for dx := 0; dx < Cell/2; dx++ {
					g.Pix[(300+row*Cell/2+dy)*w+col*Cell/2+dx] = val
				}
			}
		}
	}
	if _, err := Locate(g, v.MS, 2000); err == nil {
		t.Fatal("a half-scale strip was accepted")
	}
}
