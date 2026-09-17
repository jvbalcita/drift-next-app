package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// itemKind classifies what pressing a key does.
type itemKind int

const (
	kindAction itemKind = iota
	kindView
	kindExit
	kindRefresh
)

// itemGroup decides how a key is laid out in the rendered menu.
type itemGroup int

const (
	groupActions itemGroup = iota
	groupTroubleshooting
	groupShortcut
	// groupResolution is a decision about an address served by a process this
	// session did not start. It is rendered apart from the other groups, one item
	// per line, because it must show the holder before the choice is made and must
	// not read as an ordinary action.
	groupResolution
	// groupExit is rendered last, after every other group, so the way out sits at
	// the end of the list an operator reads rather than in the middle of it.
	groupExit
)

// menuItem is one operator key. The menu grid, the prompt key range, and
// dispatch are all derived from this, so the advertised keys can never drift
// away from the actions that exist.
type menuItem struct {
	Key      string
	Label    string
	Kind     itemKind
	Group    itemGroup
	View     viewMode
	Shortcut bool
	Run      func(context.Context, ops) error
}

// menu returns the operator surface in display order.
//
// Two properties of that order are load-bearing and asserted in
// readability_test.go rather than left to the reader:
//
//   - Keys 1..16 ascend in the order the frame reads, and Exit is last, so
//     counting down the list matches the numbering printed beside it. The
//     troubleshooting keys follow the primary actions and the way out follows
//     the work, which is why Exit carries the highest key rather than sitting in
//     the middle of the list as it did at key 10.
//   - Conventions carry no number. Overview, refresh and exit are offered as
//     keys (0, r, q) and explained in the frame's key legend, so the numbered
//     sequence stays a sequence an operator can trust.
func menu() []menuItem {
	startComponent := func(name string) func(context.Context, ops) error {
		return func(ctx context.Context, o ops) error { return o.startComponent(ctx, name) }
	}
	stopComponent := func(name string) func(context.Context, ops) error {
		return func(_ context.Context, o ops) error { return o.stopComponent(name) }
	}
	return []menuItem{
		{Key: "1", Label: "Setup Environment", Run: func(ctx context.Context, o ops) error { return o.setup(ctx) }},
		{Key: "2", Label: "Start All", Run: func(ctx context.Context, o ops) error { return o.startAll(ctx) }},
		{Key: "3", Label: "Stop All", Run: func(ctx context.Context, o ops) error { return o.stopAll(ctx) }},
		{Key: "4", Label: "Restart All", Run: func(ctx context.Context, o ops) error {
			_ = o.stopAll(ctx)
			return o.startAll(ctx)
		}},
		{Key: "5", Label: "Run All Checks", Run: func(ctx context.Context, o ops) error { return o.runChecks(ctx) }},
		{Key: "6", Label: "Build All", Run: func(ctx context.Context, o ops) error { return o.buildAll(ctx) }},
		{Key: "7", Label: "Run Real-Device Tests", Run: func(ctx context.Context, o ops) error { return o.realDeviceTests(ctx) }},
		{Key: "8", Label: "Status View", Kind: kindView, View: viewStatus},
		{Key: "9", Label: "Log View", Kind: kindView, View: viewLogs},
		{Key: "10", Label: "Start Control Plane", Group: groupTroubleshooting, Run: startComponent("Control Plane")},
		{Key: "11", Label: "Start Device Service", Group: groupTroubleshooting, Run: startComponent("Device Service")},
		{Key: "12", Label: "Start Desktop Application", Group: groupTroubleshooting, Run: startComponent("Desktop Application")},
		{Key: "13", Label: "Stop Control Plane", Group: groupTroubleshooting, Run: stopComponent("Control Plane")},
		{Key: "14", Label: "Stop Device Service", Group: groupTroubleshooting, Run: stopComponent("Device Service")},
		{Key: "15", Label: "Stop Desktop Application", Group: groupTroubleshooting, Run: stopComponent("Desktop Application")},
		{Key: "16", Label: "Exit", Kind: kindExit, Group: groupExit},
		{Key: "0", Label: "Overview", Kind: kindView, View: viewDashboard, Group: groupShortcut, Shortcut: true},
		{Key: "r", Label: "Refresh now", Kind: kindRefresh, Group: groupShortcut, Shortcut: true},
		{Key: "q", Label: "Exit", Kind: kindExit, Group: groupShortcut, Shortcut: true},
	}
}

// numberedActionItems is the single numbering source for both the frame and
// dispatch. The renderer groups items by these same groups, so build the list
// in that drawn order before assigning positions. Resolution actions therefore
// deliberately shift the keys of the troubleshooting actions and Exit.
func numberedActionItems(items []menuItem) []menuItem {
	ordered := make([]menuItem, 0, len(items))
	for _, group := range []itemGroup{groupActions, groupResolution, groupTroubleshooting, groupExit} {
		for _, item := range items {
			if !item.Shortcut && item.Group == group {
				ordered = append(ordered, item)
			}
		}
	}
	for _, item := range items {
		if item.Shortcut {
			ordered = append(ordered, item)
		}
	}
	number := 1
	for index := range ordered {
		if ordered[index].Shortcut {
			continue
		}
		ordered[index].Key = strconv.Itoa(number)
		number++
	}
	return ordered
}

// lookupItem resolves an operator key, including shortcut aliases, to its item.
func lookupItem(items []menuItem, key string) (menuItem, bool) {
	for _, item := range items {
		if item.Key == key {
			return item, true
		}
	}
	return menuItem{}, false
}

// numericBounds reports the inclusive range of numeric keys on offer.
func numericBounds(items []menuItem) (int, int, bool) {
	low, high, found := 0, 0, false
	for _, item := range items {
		number, err := strconv.Atoi(item.Key)
		if err != nil {
			continue
		}
		switch {
		case !found:
			low, high, found = number, number, true
		case number < low:
			low = number
		case number > high:
			high = number
		}
	}
	return low, high, found
}

func keyRange(items []menuItem) (int, int) {
	low, high, found := numericBounds(items)
	if !found {
		return 0, 0
	}
	return low, high
}

// promptLabel advertises exactly the keys the menu offers.
func promptLabel(items []menuItem) string {
	keys := make([]string, 0, len(items))
	if low, high, found := numericBounds(items); found {
		keys = append(keys, fmt.Sprintf("%d-%d", low, high))
	}
	shortcuts := make([]string, 0, len(items))
	for _, item := range items {
		if _, err := strconv.Atoi(item.Key); err != nil {
			shortcuts = append(shortcuts, item.Key)
		}
	}
	sort.Strings(shortcuts)
	keys = append(keys, shortcuts...)
	return fmt.Sprintf("Select an action [%s]: ", strings.Join(keys, ", "))
}
