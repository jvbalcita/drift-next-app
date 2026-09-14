package observations_test

import (
	"embed"
	"encoding/json"
	"io/fs"
	"testing"

	"drift.local/drift-next/internal/platform/redaction"
)

//go:embed *.json
var fixtureFiles embed.FS

func TestObservationFixturesAreBoundedAndSanitized(t *testing.T) {
	entries, err := fs.ReadDir(fixtureFiles, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(fixtureFiles, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > 65536 {
			t.Fatalf("fixture %q is unbounded", entry.Name())
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatalf("fixture %q is invalid JSON: %v", entry.Name(), err)
		}
		if redaction.RedactString(string(data)) != string(data) {
			t.Fatalf("fixture %q contains sensitive material", entry.Name())
		}
	}
}
