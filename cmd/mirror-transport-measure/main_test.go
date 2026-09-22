package main

import "testing"

func TestParseProcessSample(t *testing.T) {
	rss, cpu, ok := parseProcessSample("  51200  37.5\n")
	if !ok || rss != 50 || cpu != 37.5 {
		t.Fatalf("sample = %.1f MB %.1f%% %t, want 50 MB 37.5%% true", rss, cpu, ok)
	}
	for _, invalid := range []string{"", "51200", "bad 4.0", "-1 2.0", "51200 NaN", "+Inf 2.0"} {
		if _, _, ok := parseProcessSample(invalid); ok {
			t.Fatalf("invalid process sample %q was accepted", invalid)
		}
	}
}
