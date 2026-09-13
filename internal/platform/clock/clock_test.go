package clock_test

import (
	"testing"
	"time"

	"drift.local/drift-next/internal/platform/clock"
)

func TestFixedClockReturnsUTCValue(t *testing.T) {
	input := time.Date(2026, time.September, 13, 8, 30, 0, 123456789, time.FixedZone("PHT", 8*60*60))
	fixed := clock.NewFixed(input)

	got := fixed.Now()
	if !got.Equal(input) {
		t.Fatalf("Now() = %s, want %s", got, input)
	}
	if got.Location() != time.UTC {
		t.Fatalf("Now() location = %s, want UTC", got.Location())
	}
}

func TestSystemClockReturnsUTC(t *testing.T) {
	got := (clock.System{}).Now()
	if got.Location() != time.UTC {
		t.Fatalf("System.Now() location = %s, want UTC", got.Location())
	}
	if got.IsZero() {
		t.Fatal("System.Now() returned zero time")
	}
}
