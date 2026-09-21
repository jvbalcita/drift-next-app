package service_test

import (
	"testing"
	"time"

	"drift.local/drift-next/internal/media"
	"drift.local/drift-next/internal/service"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// The fleet grid's still route, from the other side of the nil case the route
// constructor already covers: absent without a capture set or a device resolver,
// present with both. A grid an operator renders and then finds dead is the outcome
// this gate exists to prevent, and a grid that draws is the outcome it exists to
// allow.

// mountGrid is the plane's capture set as this route reads it. It carries no
// behaviour: these cases are about whether the route is mounted at all.
type mountGrid struct{}

func (mountGrid) SyncSubscriptions(serials []string) media.GridSubscription {
	return media.GridSubscription{Admitted: serials}
}

func (mountGrid) Frame(string) (media.Frame, bool) {
	return media.Frame{Serial: "serial-1", CapturedAt: time.Unix(0, 0).UTC(), ContentHash: "sha256:x"}, true
}

func (mountGrid) GridCost() media.GridCost {
	return media.GridCost{Cadence: media.DefaultGridStillCadence, Profile: media.DefaultGridStillProfile(), MaxDevices: media.DefaultGridMaxDevices}
}

func (mountGrid) StopSubscriptions() int { return 0 }

// The compiler is what proves the capture set satisfies the port the route serves:
// if this stops compiling, the route has no production mount again.
var _ transportconnect.GridStills = mountGrid{}

// TestAConstructedGridMountsTheRoute is the positive half of the gate.
func TestAConstructedGridMountsTheRoute(t *testing.T) {
	route := service.GridPreviewRoute(mountGrid{}, mountSerials{}, "lab-token-value")
	if route.Path == "" {
		t.Fatal("a constructed capture set mounted no route: the grid is unreachable even though the whole path exists")
	}
	if route.Handler == nil {
		t.Fatalf("route %q carries no handler", route.Path)
	}
}

// TestAnAbsentGridMountsNothing: without a capture set or a resolver there is no
// route, not a route that answers every request with a refusal.
func TestAnAbsentGridMountsNothing(t *testing.T) {
	cases := []struct {
		name    string
		grid    transportconnect.GridStills
		serials transportconnect.DeviceSerialResolver
	}{
		{name: "no capture set", grid: nil, serials: mountSerials{}},
		{name: "no device resolver", grid: mountGrid{}, serials: nil},
		{name: "nothing at all", grid: nil, serials: nil},
		{name: "a typed-nil capture set", grid: typedNilGrid(), serials: mountSerials{}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			route := service.GridPreviewRoute(test.grid, test.serials, "lab-token-value")
			if route.Path != "" || route.Handler != nil {
				t.Fatalf("an incomplete grid mounted %q: it advertises a surface that cannot work", route.Path)
			}
		})
	}
}

// typedNilGrid is the shape a plain nil check misses: a non-nil interface holding a
// nil pointer. The route's gate is on the handler the constructor returned, so this
// must mount nothing.
func typedNilGrid() transportconnect.GridStills {
	var grid *nilGrid
	return grid
}

type nilGrid struct{}

func (*nilGrid) SyncSubscriptions([]string) media.GridSubscription { return media.GridSubscription{} }
func (*nilGrid) Frame(string) (media.Frame, bool)                  { return media.Frame{}, false }
func (*nilGrid) GridCost() media.GridCost                          { return media.GridCost{} }
func (*nilGrid) StopSubscriptions() int                            { return 0 }
