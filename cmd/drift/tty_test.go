package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestColourAndRawModeDegradeOutsideARealTerminal(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "frame.txt"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = file.Close() })

	unset := func(string) (string, bool) { return "", false }
	set := func(string) (string, bool) { return "1", true }
	blank := func(string) (string, bool) { return "", true }

	if noColorEnv(unset) {
		t.Fatal("an unset NO_COLOR disabled colour")
	}
	if !noColorEnv(set) {
		t.Fatal("NO_COLOR=1 did not disable colour")
	}
	if noColorEnv(blank) {
		t.Fatal("an empty NO_COLOR is not the documented disable signal")
	}
	if isTerminal(file) {
		t.Fatal("a regular file was treated as an interactive terminal")
	}
	if colorEnabled(file, unset) || colorEnabled(file, set) {
		t.Fatal("colour was enabled for a non-terminal output")
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })

	if isTerminal(writer) {
		t.Fatal("a pipe was treated as an interactive terminal")
	}
	if colorEnabled(writer, unset) {
		t.Fatal("colour was enabled for a pipe")
	}
	if width, height := terminalSize(writer); width != 0 || height != 0 {
		t.Fatalf("terminal size for a pipe = %dx%d, want unknown", width, height)
	}
	if _, err := enableRaw(writer); err == nil {
		t.Fatal("raw input mode was enabled for a pipe")
	}
}
