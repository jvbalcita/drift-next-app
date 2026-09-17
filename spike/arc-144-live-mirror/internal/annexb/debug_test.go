package annexb

import (
	"os"
	"testing"
)

// TestDebugSplit prints what the splitter sees, to diagnose grouping.
func TestDebugSplit(t *testing.T) {
	path := os.Getenv("SPIKE_H264")
	if path == "" {
		t.Skip("SPIKE_H264 not set")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	nals, err := findNALs(b)
	if err != nil {
		t.Fatal(err)
	}
	shown := 0
	for i, n := range nals {
		if !isVCL(n.typ) {
			continue
		}
		nal := b[n.off:n.end]
		hdr := nalHeaderOffset(nal)
		body := nal[hdr:]
		v, bits, err := readUE(body)
		t.Logf("nal %d type=%d len=%d hdr=%d firstbytes=% x first_mb=%d bits=%d err=%v newpicture=%v",
			i, n.typ, len(nal), hdr, body[:4], v, bits, err, startsNewPicture(nal))
		shown++
		if shown >= 5 {
			break
		}
	}
}
