package domain_test

import (
	"strings"
	"testing"

	"drift.local/drift-next/internal/domain"
)

// The package-name rule is shared by the observation read that captures a
// device's foreground package and the evidence boundary that admits a package
// into a record, so its shape is asserted here rather than only through its
// callers.
func TestIsPackageNameAdmitsOnlyAPackageName(t *testing.T) {
	accepted := []string{
		"com.example",
		"com.example.target",
		"a.b",
		"com.example_app.impl2",
		"Com.Example",
	}
	refused := []string{
		"",                   // nothing to carry
		"com",                // no segment after a dot
		"com.",               // a trailing dot
		".example",           // a leading dot
		"1com.example",       // a segment may not begin with a digit
		"com.example target", // whitespace
		"com.example/other",  // a path separator
		"com.example;rm",     // a shell metacharacter
		"com.example:1",      // content punctuation
		strings.Repeat("a", domain.MaxPackageNameLength),
		"com." + strings.Repeat("a", domain.MaxPackageNameLength),
	}
	for _, value := range accepted {
		if !domain.IsPackageName(value) {
			t.Errorf("IsPackageName(%q) = false, want true", value)
		}
	}
	for _, value := range refused {
		if domain.IsPackageName(value) {
			t.Errorf("IsPackageName(%q) = true, want false", value)
		}
	}
}
