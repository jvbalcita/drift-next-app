// Package clockcode is the wire format between the device's own pixels and the
// host that measures them.
//
// Part B has no camera, so the only clock that can time the glass is one the
// device paints into its own screen: a millisecond timestamp carried as a
// black/white cell grid. This package is the Go half of that codec; the device
// page in cmd/live/web/clock.html is the JavaScript half, and the two are held
// together by the constants below and by internal/clockcode's tests.
//
// Layout (origin at the strip's top-left corner):
//
//	bit 0..7    sync, alternating 10101010
//	bit 8..55   the millisecond timestamp, 48 bits, LSB first
//	bit 56      reacting: the page is showing a reaction to an injected tap
//	bit 57..58  tap count, 2 bits, LSB first (wraps every four taps)
//	bit 59      even parity over bits 0..58
//
// Bits are laid out row-major: bit = row*CellsX + col. One cell is Cell pixels
// square, so the whole strip is Width x Height and is drawn at the viewport's
// top-left corner.
package clockcode

import (
	"fmt"
	"math"
)

// Strip geometry. The device page draws exactly this, and the browser probe
// reads exactly this.
const (
	CellsX = 30
	CellsY = 2
	Cell   = 32
	Width  = CellsX * Cell
	Height = CellsY * Cell

	Bits      = CellsX * CellsY // 60
	SyncBits  = 8
	MSBits    = 48
	FlagBits  = 3
	ParityPos = SyncBits + MSBits + FlagBits // 59
)

// SyncPattern is the alternating prefix every valid strip starts with.
var SyncPattern = [SyncBits]int{1, 0, 1, 0, 1, 0, 1, 0}

// Value is what one strip carries.
type Value struct {
	MS       int64 `json:"ms"`
	Reacting bool  `json:"reacting"`
	TapCount int   `json:"tap_count"`
}

// Bits returns the 60 cell values (1 = white, 0 = black) for v.
func (v Value) Bits() []int {
	bits := make([]int, Bits)
	copy(bits, SyncPattern[:])
	for i := 0; i < MSBits; i++ {
		bits[SyncBits+i] = int((v.MS >> uint(i)) & 1)
	}
	if v.Reacting {
		bits[SyncBits+MSBits] = 1
	}
	bits[SyncBits+MSBits+1] = v.TapCount & 1
	bits[SyncBits+MSBits+2] = (v.TapCount >> 1) & 1
	parity := 0
	for i := 0; i < ParityPos; i++ {
		parity ^= bits[i]
	}
	bits[ParityPos] = parity
	return bits
}

// DecodeBits validates a strip's cell values and returns what it carries.
//
// It fails rather than guessing on a sync or parity mismatch, so a torn or
// misaligned read is reported as no sample instead of a plausible wrong
// timestamp.
func DecodeBits(bits []int) (Value, error) {
	if len(bits) != Bits {
		return Value{}, fmt.Errorf("clockcode: %d bits, want %d", len(bits), Bits)
	}
	for i, want := range SyncPattern {
		if bits[i] != want {
			return Value{}, fmt.Errorf("clockcode: sync mismatch at cell %d", i)
		}
	}
	parity := 0
	for i := 0; i < ParityPos; i++ {
		parity ^= bits[i]
	}
	if parity != bits[ParityPos] {
		return Value{}, fmt.Errorf("clockcode: parity mismatch")
	}
	var ms int64
	for i := 0; i < MSBits; i++ {
		if bits[SyncBits+i] == 1 {
			ms |= 1 << uint(i)
		}
	}
	return Value{
		MS:       ms,
		Reacting: bits[SyncBits+MSBits] == 1,
		TapCount: bits[SyncBits+MSBits+1] | bits[SyncBits+MSBits+2]<<1,
	}, nil
}

// Gray is a single-channel image: the device framebuffer or a decoded video
// frame, reduced to the luma the black/white strip is carried in.
type Gray struct {
	Pix    []uint8 `json:"-"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
}

// At returns the luma at (x, y), or an error when out of bounds.
func (g Gray) At(x, y int) (uint8, error) {
	if x < 0 || y < 0 || x >= g.Width || y >= g.Height {
		return 0, fmt.Errorf("clockcode: (%d,%d) outside %dx%d", x, y, g.Width, g.Height)
	}
	return g.Pix[y*g.Width+x], nil
}

// ReadBits samples the strip whose top-left corner is at (x0, y0) and returns
// its 60 cell values, reading the centre pixel of each cell.
func ReadBits(g Gray, x0, y0 int) ([]int, error) {
	bits := make([]int, Bits)
	for row := 0; row < CellsY; row++ {
		for col := 0; col < CellsX; col++ {
			px := x0 + col*Cell + Cell/2
			py := y0 + row*Cell + Cell/2
			v, err := g.At(px, py)
			if err != nil {
				return nil, err
			}
			if v >= 128 {
				bits[row*CellsX+col] = 1
			}
		}
	}
	return bits, nil
}

// ReadValue samples and decodes the strip at (x0, y0).
func ReadValue(g Gray, x0, y0 int) (Value, error) {
	bits, err := ReadBits(g, x0, y0)
	if err != nil {
		return Value{}, err
	}
	return DecodeBits(bits)
}

// Location is where the strip turned out to be, and what it said.
type Location struct {
	X         int    `json:"x"`
	Y         int    `json:"y"`
	MS        int64  `json:"ms"`
	Offset    int64  `json:"offset_from_reference_ms"`
	React     bool   `json:"reacting"`
	TapCnt    int    `json:"tap_count"`
	DarkAbove int    `json:"luma_above_origin"`
	TopRun    int    `json:"white_run_rows_above_origin"`
	Method    string `json:"method"`
}

// minTopRun is how much of the first cell must be visible for the origin to be
// pinned. A cell is Cell rows; the browser may round the strip's device height by
// a pixel or two, so a couple of rows of slack keep the test from failing on a
// rounding artefact while staying far tighter than the Cell/2 ambiguity a
// centre-sampling locator has.
const minTopRun = Cell - 6

// Locate finds the strip's origin in one frame.
//
// The page pins the strip to the left edge of its viewport with a black page
// background and one clear cell above its top-left cell (the first strip cell is
// white by construction: sync bit 0 is 1). Its origin is therefore the one place
// where a dark pixel sits directly above a run of white running the full height
// of a cell. Searching for that boundary pins the origin to within a couple of
// pixels, where centring a sampled grid only pins it to within half a cell --
// and half a cell of error is the difference between reading the strip and
// reading its neighbour.
//
// The decode at the found origin must also validate and its timestamp must be
// within tolerance of referenceMS, so a white toolbar cannot be mistaken for a
// clock.
func Locate(g Gray, referenceMS int64, toleranceMS int64) (Location, error) {
	probeX := Cell / 2
	attempts := 0
	for y0 := 1; y0+Height <= g.Height; y0++ {
		above, err := g.At(probeX, y0-1)
		if err != nil || above > darkMax {
			continue
		}
		run := 0
		for dy := 0; dy < Cell; dy++ {
			v, err := g.At(probeX, y0+dy)
			if err != nil || v < whiteMin {
				break
			}
			run++
		}
		if run < minTopRun {
			continue
		}
		attempts++
		v, err := ReadValue(g, 0, y0)
		if err != nil {
			continue
		}
		if diff := v.MS - referenceMS; diff > toleranceMS || diff < -toleranceMS {
			continue
		}
		return Location{
			X: 0, Y: y0, MS: v.MS, Offset: v.MS - referenceMS,
			React: v.Reacting, TapCnt: v.TapCount,
			DarkAbove: int(above), TopRun: run, Method: "top-edge",
		}, nil
	}
	return Location{}, fmt.Errorf("clockcode: no strip found in %dx%d (%d top-edge candidates decoded)", g.Width, g.Height, attempts)
}

// LocateTolerant is the fallback locator: it samples cell centres over a window
// and reports the median origin of every candidate that decodes.
//
// It is used only to say where the strip roughly is when the exact rule fails
// (for example if the page's layout moved), and its accuracy is half a cell, so
// a caller that reads bits per cell must not use it for the measurement.
func LocateTolerant(g Gray, referenceMS int64, toleranceMS int64, maxX, maxY int) (Location, error) {
	if maxX <= 0 {
		maxX = 64
	}
	if maxY <= 0 || maxY > g.Height-Height {
		maxY = g.Height - Height
	}
	var xs, ys []int
	var last Value
	for y := 0; y <= maxY; y++ {
		for x := 0; x <= maxX; x++ {
			v, err := ReadValue(g, x, y)
			if err != nil {
				continue
			}
			if diff := v.MS - referenceMS; diff > toleranceMS || diff < -toleranceMS {
				continue
			}
			xs = append(xs, x)
			ys = append(ys, y)
			last = v
		}
	}
	if len(xs) == 0 {
		return Location{}, fmt.Errorf("clockcode: no candidate decoded in %dx%d", g.Width, g.Height)
	}
	return Location{
		X: xs[len(xs)/2], Y: ys[len(ys)/2],
		MS: last.MS, React: last.Reacting, TapCnt: last.TapCount,
		Method: fmt.Sprintf("tolerant(%d candidates)", len(xs)),
	}, nil
}

// Thresholds for the black/white strip. The device page paints pure black and
// pure white; these bounds leave room for H.264 ringing at the cell edges.
const (
	whiteMin = 160
	darkMax  = 96
)

// Stats summarises a series of decoded timestamps, which is the evidence that
// the strip being read really is a clock and not a pattern.
type Stats struct {
	Samples     int   `json:"samples"`
	MinDelta    int64 `json:"min_delta_ms"`
	MedianDelta int64 `json:"median_delta_ms"`
	MaxDelta    int64 `json:"max_delta_ms"`
	Unique      int   `json:"unique_values"`
	Backwards   int   `json:"non_monotonic"`
	SpanMS      int64 `json:"span_ms"`
}

// Summarise reports how a series of timestamps advanced, so a caller can assert
// monotonicity and the sampling cadence instead of trusting it.
func Summarise(values []int64) Stats {
	if len(values) == 0 {
		return Stats{}
	}
	uniq := map[int64]struct{}{}
	deltas := make([]int64, 0, len(values))
	backwards := 0
	for i, v := range values {
		uniq[v] = struct{}{}
		if i > 0 {
			d := v - values[i-1]
			if d < 0 {
				backwards++
			}
			deltas = append(deltas, d)
		}
	}
	sorted := append([]int64(nil), deltas...)
	sortInt64(sorted)
	st := Stats{
		Samples:   len(values),
		Unique:    len(uniq),
		Backwards: backwards,
		SpanMS:    values[len(values)-1] - values[0],
	}
	if len(sorted) > 0 {
		st.MinDelta = sorted[0]
		st.MaxDelta = sorted[len(sorted)-1]
		st.MedianDelta = sorted[len(sorted)/2]
	}
	return st
}

func sortInt64(v []int64) {
	// Insertion sort: these series are ten to a few hundred long, and the
	// package should not need the sort package for one call site.
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

// Percentile returns the p-th percentile (0..100) of v using the nearest-rank
// method, which is what the Part A analysis used, so numbers stay comparable.
func Percentile(v []int64, p float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	sorted := append([]int64(nil), v...)
	sortInt64(sorted)
	if p <= 0 {
		return float64(sorted[0])
	}
	if p >= 100 {
		return float64(sorted[len(sorted)-1])
	}
	rank := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return float64(sorted[rank])
}
