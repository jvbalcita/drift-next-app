package ids_test

import (
	"errors"
	"io"
	"regexp"
	"testing"

	"drift.local/drift-next/internal/platform/ids"
)

func TestRandomGeneratorReturnsStableTextIDAndPropagatesEntropyFailure(t *testing.T) {
	generator := ids.NewRandom()
	first, err := generator.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(first) {
		t.Fatalf("NewID() = %q, want a UUID v4 text ID", first)
	}

	failing, err := ids.NewRandomWithReader(errorReader{})
	if err != nil {
		t.Fatalf("NewRandomWithReader() error = %v", err)
	}
	if _, err := failing.NewID(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("NewID() error = %v, want wrapped entropy error", err)
	}
}

func TestSequenceGeneratorIsDeterministicAndReportsExhaustion(t *testing.T) {
	generator := ids.NewSequence("device-a", "device-b")

	for index, want := range []string{"device-a", "device-b"} {
		got, err := generator.NewID()
		if err != nil {
			t.Fatalf("NewID() call %d error = %v", index+1, err)
		}
		if got != want {
			t.Fatalf("NewID() call %d = %q, want %q", index+1, got, want)
		}
	}
	if _, err := generator.NewID(); !errors.Is(err, ids.ErrExhausted) {
		t.Fatalf("NewID() after sequence error = %v, want ErrExhausted", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
