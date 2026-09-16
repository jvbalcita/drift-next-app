package domain

import "regexp"

// MaxPackageNameLength bounds a package name carried through an observation or
// an evidence record.
const MaxPackageNameLength = 255

// packageNamePattern is the shape of an Android package name: dot-separated
// segments of letters, digits and underscores, beginning with a letter. It
// admits no whitespace, quote, flag, path separator or shell metacharacter, so
// a value that passes this pattern can never express anything but a package
// name.
var packageNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$`)

// IsPackageName reports whether value is a package name.
//
// The rule lives here because two boundaries need the same answer: the
// observation read that captures a device's foreground package, and the
// evidence boundary that admits a package into an append-only record. A device
// names its own package in its own output, so both treat the value as untrusted
// input rather than as a fact, and a value that is not a package name is
// refused by both instead of being carried.
func IsPackageName(value string) bool {
	if value == "" || len(value) > MaxPackageNameLength {
		return false
	}
	return packageNamePattern.MatchString(value)
}
