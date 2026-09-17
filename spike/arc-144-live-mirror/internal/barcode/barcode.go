// Package barcode encodes and decodes the machine-readable frame counter that
// this spike burns into the top-left corner of every synthetic video frame.
//
// The counter is what makes the latency numbers measurable: a decoded frame
// carries its own source index in its pixels, so the browser can report which
// source frame it is displaying without any clock shared with the encoder, and
// a reader can join that to the sender's per-frame send timestamp.
//
// Layout: 20 cells of 32x32 px starting at (48, 48).
//   - cells 0..15  : frame index, least significant bit first
//   - cells 16..19 : fixed sync pattern 1,0,1,0
//
// The sync pattern is what lets a decoder reject a garbage read (a motion
// blur, a scaled readback, a mis-cropped region) instead of silently reporting
// a plausible wrong frame number.
package barcode

// IndexBits is the number of bits used for the frame index.
const IndexBits = 16

// SyncBits is the number of trailing sync bits.
const SyncBits = 4

// TotalBits is the full barcode width in cells.
const TotalBits = IndexBits + SyncBits

// CellSize is the side of one cell in pixels.
const CellSize = 32

// OriginX and OriginY are the top-left pixel of cell 0.
const (
	OriginX = 48
	OriginY = 48
)

// Luma values used for the two states. Full-range-ish contrast so that a
// readback threshold of 128 is never close to a decision boundary.
const (
	DarkY  = 16
	LightY = 235
)

// SyncPattern is the fixed trailing pattern, cell 16..19.
var SyncPattern = [SyncBits]int{1, 0, 1, 0}

// MaxIndex is the largest frame index the barcode can carry.
const MaxIndex = 1<<IndexBits - 1

// Width is the barcode width in pixels.
func Width() int { return TotalBits * CellSize }

// Height is the barcode height in pixels.
func Height() int { return CellSize }

// Bit returns the i'th barcode cell value (0 or 1) for a frame index.
func Bit(index, i int) int {
	if i < IndexBits {
		return (index >> uint(i)) & 1
	}
	if j := i - IndexBits; j < SyncBits {
		return SyncPattern[j]
	}
	return 0
}

// DrawY paints the barcode for index into a Y plane of the given dimensions.
// It also paints an opaque backing rectangle with a margin so the barcode
// never sits on top of moving scene content.
func DrawY(y []byte, stride, w, h, index int) {
	margin := 16
	fillRect(y, stride, w, h, OriginX-margin, OriginY-margin, Width()+2*margin, Height()+2*margin, DarkY)
	for i := 0; i < TotalBits; i++ {
		v := byte(DarkY)
		if Bit(index, i) == 1 {
			v = LightY
		}
		fillRect(y, stride, w, h, OriginX+i*CellSize, OriginY, CellSize, CellSize, v)
	}
}

func fillRect(y []byte, stride, w, h, x0, y0, rw, rh int, v byte) {
	for yy := y0; yy < y0+rh; yy++ {
		if yy < 0 || yy >= h {
			continue
		}
		row := y[yy*stride : yy*stride+stride]
		for xx := x0; xx < x0+rw; xx++ {
			if xx < 0 || xx >= w {
				continue
			}
			row[xx] = v
		}
	}
}

// DecodeY reads the barcode out of a Y plane, returning the index and whether
// the sync pattern validated the read.
//
// Each cell is sampled as the mean of its centre 8x8 block, which is what the
// browser-side reader does too (via a canvas readback), so an encode/decode of
// a generated frame and a browser readback of the same frame are checked with
// the same rule.
func DecodeY(y []byte, stride, w, h int) (int, bool) {
	const probe = 8
	const offset = (CellSize - probe) / 2

	index := 0
	for i := 0; i < TotalBits; i++ {
		cx := OriginX + i*CellSize + offset
		cy := OriginY + offset
		sum, n := 0, 0
		for yy := cy; yy < cy+probe; yy++ {
			if yy < 0 || yy >= h {
				continue
			}
			row := y[yy*stride : yy*stride+stride]
			for xx := cx; xx < cx+probe; xx++ {
				if xx < 0 || xx >= w {
					continue
				}
				sum += int(row[xx])
				n++
			}
		}
		if n == 0 {
			return 0, false
		}
		bit := 0
		if sum/n >= 128 {
			bit = 1
		}
		if i < IndexBits {
			index |= bit << uint(i)
			continue
		}
		if bit != SyncPattern[i-IndexBits] {
			return 0, false
		}
	}
	return index, true
}
