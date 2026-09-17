// Package webui holds the two pages of the Part B rig: the browser probe the
// console-side observer runs, and the clock page the device displays.
//
// They live here rather than beside one command because two commands serve
// them: cmd/live (the device rig) and cmd/rtpreplay (the same hop, replaying a
// captured stream with no device attached), and a page that differed between
// them would make a replay an invalid check of the live path.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed web
var files embed.FS

// FS returns the pages rooted at their own directory.
func FS() fs.FS {
	sub, err := fs.Sub(files, "web")
	if err != nil {
		panic(err)
	}
	return sub
}
