package main

import (
	"context"
	"fmt"
	"strconv"

	"drift.local/drift-next/internal/runtime"
)

// ResolutionItems offers the two resolutions for every address served by a
// process this session did not start: adopt it, or terminate it and start ours.
//
// The choice is offered as menu items rather than as a prompt state machine on
// purpose. It is then the same kind of decision as every other operator action:
// it exists only while the condition does, it names the holder before the choice
// is made, the frame shows exactly which key does which thing, and doing nothing
// is not one of the options. Pressing no key leaves the runtime untouched and is
// not a failure - there is nothing to cancel, and nothing to time out.
//
// Both resolutions are per address. The holder travels in the label because an
// operator cannot decide between adopting and killing a process they have not
// been shown.
func resolutionItems(external []runtime.ExternalComponent) []menuItem {
	if len(external) == 0 {
		return nil
	}
	key := nextResolutionKey()
	items := make([]menuItem, 0, len(external)*2)
	for _, component := range external {
		target := component
		adoptKey := strconv.Itoa(key)
		terminateKey := strconv.Itoa(key + 1)
		key += 2
		items = append(items,
			menuItem{
				Key:   adoptKey,
				Label: fmt.Sprintf("Adopt %s: keep %s on %s, revision unverified", target.Name, target.Holder, target.Address),
				Group: groupResolution,
				Run:   func(_ context.Context, o ops) error { return o.adopt(target.Name) },
			},
			menuItem{
				Key:   terminateKey,
				Label: fmt.Sprintf("Terminate %s holder on %s (%s) and start this session's own", target.Name, target.Address, target.Holder),
				Group: groupResolution,
				Run:   func(ctx context.Context, o ops) error { return o.terminate(ctx, target.Name) },
			},
		)
	}
	return items
}

// nextResolutionKey returns the first key after every key the menu already
// advertises, so a resolution can never take a key that already means something
// else.
func nextResolutionKey() int {
	_, high, found := numericBounds(menu())
	if !found {
		return 1
	}
	return high + 1
}
