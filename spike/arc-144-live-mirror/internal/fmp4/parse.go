// Package fmp4 parses a fragmented MP4 (CMAF-style) file produced by ffmpeg
// with `-c copy` from the same H.264 access units the RTP path sends.
//
// It exposes the init segment and one record per moof+mdat fragment together
// with the source frame range the fragment covers, so the MSE consumer can be
// delivered exactly the bytes that belong to a run of frames and the latency
// of that transport can be joined to the same frame indices the RTP path uses.
package fmp4

import (
	"encoding/binary"
	"fmt"
)

// Fragment is one moof+mdat pair.
type Fragment struct {
	Index      int
	StartFrame int
	EndFrame   int // exclusive
	Data       []byte
	DTS        uint64
}

// File is the parsed stream.
type File struct {
	Init      []byte
	Fragments []Fragment
	Timescale uint32
}

type box struct {
	typ  string
	off  int
	size int
}

// Parse walks the file and returns the init segment plus its fragments.
func Parse(b []byte, fps int) (*File, error) {
	f := &File{Timescale: 0}
	boxes, err := topLevelBoxes(b)
	if err != nil {
		return nil, err
	}
	initEnd := -1
	nextFrame := 0
	for i := 0; i < len(boxes); i++ {
		bx := boxes[i]
		switch bx.typ {
		case "moof":
			if initEnd < 0 {
				initEnd = bx.off
			}
			if i+1 >= len(boxes) || boxes[i+1].typ != "mdat" {
				return nil, fmt.Errorf("moof at %d is not followed by mdat", bx.off)
			}
			mdat := boxes[i+1]
			body := b[bx.off : bx.off+bx.size]
			samples, dts, err := moofInfo(body)
			if err != nil {
				return nil, fmt.Errorf("moof at %d: %w", bx.off, err)
			}
			frag := Fragment{
				Index:      len(f.Fragments),
				StartFrame: nextFrame,
				EndFrame:   nextFrame + samples,
				DTS:        dts,
				Data:       b[bx.off : mdat.off+mdat.size],
			}
			if f.Timescale != 0 {
				want := int(dts * uint64(fps) / uint64(f.Timescale))
				if want != nextFrame {
					return nil, fmt.Errorf("fragment %d: tfdt says frame %d, sample count says %d", frag.Index, want, nextFrame)
				}
			}
			f.Fragments = append(f.Fragments, frag)
			nextFrame += samples
			i++ // consume the mdat
		case "ftyp", "moov", "free", "styp", "sidx", "udta":
			if ts := timescaleOf(b[bx.off : bx.off+bx.size]); ts != 0 {
				f.Timescale = ts
			}
		}
	}
	if initEnd < 0 {
		return nil, fmt.Errorf("no moof boxes: not a fragmented mp4")
	}
	if f.Timescale == 0 {
		return nil, fmt.Errorf("no mdhd timescale found")
	}
	f.Init = b[:initEnd]
	if len(f.Fragments) == 0 {
		return nil, fmt.Errorf("no fragments parsed")
	}
	return f, nil
}

// moofInfo returns the sample count and base media decode time of a moof box.
func moofInfo(moof []byte) (samples int, dts uint64, err error) {
	for _, bx := range children(moof) {
		if bx.typ != "traf" {
			continue
		}
		for _, in := range children(moof[bx.off : bx.off+bx.size]) {
			body := moof[bx.off+in.off : bx.off+in.off+in.size]
			switch in.typ {
			case "tfdt":
				if len(body) < 12 {
					return 0, 0, fmt.Errorf("short tfdt")
				}
				version := body[8]
				if version == 1 {
					if len(body) < 20 {
						return 0, 0, fmt.Errorf("short tfdt v1")
					}
					dts = binary.BigEndian.Uint64(body[12:20])
				} else {
					dts = uint64(binary.BigEndian.Uint32(body[12:16]))
				}
			case "trun":
				// version(1)+flags(3)+sample_count(4) after the 8-byte header.
				if len(body) < 16 {
					return 0, 0, fmt.Errorf("short trun (%d bytes)", len(body))
				}
				samples += int(binary.BigEndian.Uint32(body[12:16]))
			}
		}
	}
	if samples == 0 {
		return 0, 0, fmt.Errorf("no trun sample count")
	}
	return samples, dts, nil
}

func timescaleOf(b []byte) uint32 {
	boxes := children(b)
	for _, bx := range boxes {
		if bx.typ != "trak" {
			continue
		}
		sub := b[bx.off : bx.off+bx.size]
		for _, mdia := range children(sub) {
			if mdia.typ != "mdia" {
				continue
			}
			mdiaBody := sub[mdia.off : mdia.off+mdia.size]
			for _, mdhd := range children(mdiaBody) {
				if mdhd.typ != "mdhd" {
					continue
				}
				body := mdiaBody[mdhd.off : mdhd.off+mdhd.size]
				if len(body) < 20 {
					continue
				}
				if body[8] == 1 {
					if len(body) < 32 {
						continue
					}
					return binary.BigEndian.Uint32(body[28:32])
				}
				return binary.BigEndian.Uint32(body[20:24])
			}
		}
	}
	return 0
}

// children lists the direct children of a container box body (b starts at the
// box header of the container).
func children(b []byte) []box {
	if len(b) < 8 {
		return nil
	}
	hdr := 8
	if binary.BigEndian.Uint32(b[0:4]) == 1 {
		hdr = 16
	}
	if hdr > len(b) {
		return nil
	}
	var out []box
	off := hdr
	for off+8 <= len(b) {
		size := int(binary.BigEndian.Uint32(b[off : off+4]))
		typ := string(b[off+4 : off+8])
		body := 8
		if size == 1 {
			if off+16 > len(b) {
				break
			}
			size64 := binary.BigEndian.Uint64(b[off+8 : off+16])
			if size64 > uint64(len(b)-off) {
				break
			}
			size = int(size64)
			body = 16
		} else if size == 0 {
			size = len(b) - off
		}
		if size < body || off+size > len(b) {
			break
		}
		out = append(out, box{typ: typ, off: off, size: size})
		off += size
	}
	return out
}

func topLevelBoxes(b []byte) ([]box, error) {
	if len(b) < 8 {
		return nil, fmt.Errorf("file too short")
	}
	var out []box
	off := 0
	for off+8 <= len(b) {
		size := int(binary.BigEndian.Uint32(b[off : off+4]))
		typ := string(b[off+4 : off+8])
		if size == 1 {
			if off+16 > len(b) {
				return nil, fmt.Errorf("truncated 64-bit box at %d", off)
			}
			size = int(binary.BigEndian.Uint64(b[off+8 : off+16]))
		} else if size == 0 {
			size = len(b) - off
		}
		if size < 8 || off+size > len(b) {
			return nil, fmt.Errorf("box %q at %d has bad size %d (file %d)", typ, off, size, len(b))
		}
		out = append(out, box{typ: typ, off: off, size: size})
		off += size
	}
	return out, nil
}
