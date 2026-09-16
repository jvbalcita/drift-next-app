package runtime

import (
	"fmt"
	"strconv"
	"strings"
)

// Resolving the processes bound to an address lives here, separate from the
// supervisor's decisions, so that naming a holder for the operator and listing
// the holders to act on are the same parse of the same output. Two parsers would
// eventually disagree, and the disagreement would be an operator terminating a
// different process from the one they were shown.

// parseListenerHolders parses `lsof -Fpc` output into the listening processes.
//
// An entry without a usable pid is dropped: a command name on its own cannot be
// named precisely, cannot be signalled, and must never be presented as something
// the operator can act on.
func parseListenerHolders(output string) []listener {
	var listeners []listener
	command, pid := "", ""
	flush := func() {
		number, err := strconv.Atoi(pid)
		if pid == "" || err != nil {
			command, pid = "", ""
			return
		}
		listeners = append(listeners, listener{PID: number, Command: command})
		command, pid = "", ""
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "p"):
			flush()
			pid = strings.TrimPrefix(line, "p")
		case strings.HasPrefix(line, "c"):
			command = strings.TrimPrefix(line, "c")
		}
	}
	flush()
	return listeners
}

// describeListeners renders listening processes for an operator. It is a
// description, so it names a few and counts the rest rather than growing the
// surface without limit.
func describeListeners(listeners []listener) string {
	holders := make([]string, 0, len(listeners))
	for _, entry := range listeners {
		if entry.Command != "" {
			holders = append(holders, fmt.Sprintf("%s (pid %d)", entry.Command, entry.PID))
			continue
		}
		holders = append(holders, fmt.Sprintf("pid %d", entry.PID))
	}
	switch len(holders) {
	case 0:
		return ""
	case 1:
		return holders[0]
	default:
		return fmt.Sprintf("%s and %d more", holders[0], len(holders)-1)
	}
}

// formatListenerHolders parses `lsof -Fpc` output into a short description of
// the listening processes, for example "node (pid 4711)".
func formatListenerHolders(output string) string {
	return describeListeners(parseListenerHolders(output))
}
