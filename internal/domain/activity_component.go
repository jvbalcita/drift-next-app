package domain

import (
	"regexp"
	"strings"
)

// MaxActivityComponentLength bounds an activity component name carried to a
// device input.
const MaxActivityComponentLength = 255

// activityComponentPattern is the character set of an activity component: it
// admits letters, digits, underscores and dots, and nothing else. No
// whitespace, quote, flag, path separator or shell metacharacter can pass it,
// so a value that passes this pattern can never express a second token.
var activityComponentPattern = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)

// IsActivityComponent reports whether value names a bounded activity component.
//
// The rule lives beside IsPackageName because more than one boundary needs the
// same answer: the action contract that admits a launch target, the device
// input boundary that validates the typed payload, and the adapter that turns
// the target into an allow-listed argument array. A component is a name, never
// command text, so the value must be qualified — it contains a dot — and must
// contain no empty segment, which is what stops a relative path or a component
// abbreviation from being carried as if it named an activity.
func IsActivityComponent(value string) bool {
	if value == "" || len(value) > MaxActivityComponentLength {
		return false
	}
	return activityComponentPattern.MatchString(value) && strings.Contains(value, ".") && !strings.Contains(value, "..")
}
