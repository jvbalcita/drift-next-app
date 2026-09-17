// Package annexb splits an H.264 Annex-B elementary stream into access units.
//
// One access unit is one coded picture: that is the unit the sender hands to
// the RTP packetiser (one RTP timestamp) and the unit the barcode indexes. An
// off-by-one here would shift every latency measurement, so the splitter
// groups slices the way the standard defines them -- a new picture starts at a
// slice whose first_mb_in_slice is 0 -- rather than assuming one slice per
// picture.
package annexb

import "fmt"

// NAL unit types we care about.
const (
	nalSlice    = 1
	nalIDR      = 5
	nalSEI      = 6
	nalSPS      = 7
	nalPPS      = 8
	nalAUD      = 9
	nalPrefix   = 14
	nalSliceExt = 20
)

// AccessUnit is one coded picture: its index in the stream and its Annex-B
// bytes (start codes included, as pion's payloader expects them).
type AccessUnit struct {
	Index int
	Data  []byte
}

// nalRange is one NAL unit's location in the source buffer.
type nalRange struct {
	off int // offset of the start code
	end int // end of the NAL payload
	typ int
}

// Split returns one AccessUnit per coded picture, in stream order.
func Split(b []byte) ([]AccessUnit, error) {
	nals, err := findNALs(b)
	if err != nil {
		return nil, err
	}
	var out []AccessUnit
	curStart := -1
	sawVCL := false
	flush := func(end int) {
		if curStart < 0 {
			return
		}
		out = append(out, AccessUnit{Index: len(out), Data: b[curStart:end]})
		curStart = -1
		sawVCL = false
	}
	for _, n := range nals {
		if isVCL(n.typ) && sawVCL && startsNewPicture(b[n.off:n.end]) {
			flush(n.off)
		}
		if curStart < 0 {
			curStart = n.off
		}
		if isVCL(n.typ) {
			sawVCL = true
		}
	}
	flush(len(b))
	if len(out) == 0 {
		return nil, fmt.Errorf("no access units found in %d bytes", len(b))
	}
	return out, nil
}

func isVCL(typ int) bool {
	switch typ {
	case nalSlice, nalIDR, nalSliceExt:
		return true
	}
	return false
}

// startsNewPicture reports whether a VCL NAL begins a new coded picture, which
// the standard defines as first_mb_in_slice == 0.
func startsNewPicture(nal []byte) bool {
	if len(nal) < 1 {
		return true
	}
	// Skip the start code and the 1-byte NAL header; the slice header (and so
	// first_mb_in_slice) starts at the byte after the NAL header.
	hdr := nalHeaderOffset(nal)
	if hdr < 0 || hdr+1 >= len(nal) {
		return true
	}
	body := nal[hdr+1:]
	if len(body) < 1 {
		return true
	}
	v, _, err := readUE(body)
	if err != nil {
		return true
	}
	return v == 0
}

func nalHeaderOffset(nal []byte) int {
	switch {
	case len(nal) >= 4 && nal[0] == 0 && nal[1] == 0 && nal[2] == 0 && nal[3] == 1:
		return 4
	case len(nal) >= 3 && nal[0] == 0 && nal[1] == 0 && nal[2] == 1:
		return 3
	}
	return -1
}

// readUE reads an unsigned Exp-Golomb code at the start of b.
func readUE(b []byte) (int, int, error) {
	bitPos := 0
	zeros := 0
	for {
		if bitPos/8 >= len(b) {
			return 0, bitPos, fmt.Errorf("truncated exp-golomb")
		}
		bit := (b[bitPos/8] >> uint(7-bitPos%8)) & 1
		bitPos++
		if bit == 1 {
			break
		}
		zeros++
		if zeros > 31 {
			return 0, bitPos, fmt.Errorf("exp-golomb prefix too long")
		}
	}
	val := 1
	for i := 0; i < zeros; i++ {
		if bitPos/8 >= len(b) {
			return 0, bitPos, fmt.Errorf("truncated exp-golomb value")
		}
		bit := (b[bitPos/8] >> uint(7-bitPos%8)) & 1
		bitPos++
		val = val<<1 | int(bit)
	}
	return val - 1, bitPos, nil
}

func findNALs(b []byte) ([]nalRange, error) {
	var out []nalRange
	i := 0
	for {
		start := -1
		scLen := 0
		for i+3 <= len(b) {
			if b[i] == 0 && b[i+1] == 0 {
				if b[i+2] == 1 {
					start, scLen = i, 3
					break
				}
				if i+4 <= len(b) && b[i+2] == 0 && b[i+3] == 1 {
					start, scLen = i, 4
					break
				}
			}
			i++
		}
		if start < 0 {
			break
		}
		hdr := start + scLen
		if hdr >= len(b) {
			return nil, fmt.Errorf("truncated nal header at %d", start)
		}
		// Next start code bounds this NAL.
		next := len(b)
		j := hdr + 1
		for j+3 <= len(b) {
			if b[j] == 0 && b[j+1] == 0 && (b[j+2] == 1 || (j+4 <= len(b) && b[j+2] == 0 && b[j+3] == 1)) {
				next = j
				break
			}
			j++
		}
		// Trailing zero bytes belong to the next start code, not the payload.
		end := next
		for end > hdr+1 && b[end-1] == 0 {
			end--
		}
		out = append(out, nalRange{off: start, end: end, typ: int(b[hdr] & 0x1F)})
		i = next
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no NAL units found in %d bytes", len(b))
	}
	return out, nil
}

// NAL is one NAL unit with its type and payload (the bytes after the NAL
// header, i.e. the RBSP).
type NAL struct {
	Type    int
	Payload []byte
	Bytes   int
}

// NALs returns the NAL units of an Annex-B buffer in stream order.
func NALs(b []byte) ([]NAL, error) {
	nals, err := findNALs(b)
	if err != nil {
		return nil, err
	}
	out := make([]NAL, 0, len(nals))
	for _, n := range nals {
		nal := b[n.off:n.end]
		hdr := nalHeaderOffset(nal)
		if hdr < 0 || hdr >= len(nal) {
			continue
		}
		out = append(out, NAL{Type: n.typ, Payload: nal[hdr+1:], Bytes: len(nal)})
	}
	return out, nil
}

// CountVCL counts the VCL NAL units, which is the picture count only when the
// stream carries exactly one slice per picture. Used by the verifier to
// cross-check the grouping.
func CountVCL(b []byte) (int, error) {
	nals, err := findNALs(b)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, nal := range nals {
		if isVCL(nal.typ) {
			n++
		}
	}
	return n, nil
}
