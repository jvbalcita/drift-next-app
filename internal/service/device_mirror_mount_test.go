package service_test

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/media"
	"drift.local/drift-next/internal/service"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// The live mirror route's mount gate, from the other side of the nil case the
// route constructor already covers: absent without a stream transport or a device
// resolver, present with both. A surface an operator renders and then finds dead
// is the outcome this gate exists to prevent.

// mountMirrorStream is one stream as the surface sees it. It carries no behaviour:
// these cases are about whether the route is mounted at all.
type mountMirrorStream struct{ key string }

func (s mountMirrorStream) StreamKey() string { return s.key }

func (mountMirrorStream) Answer(context.Context, string) (string, error) { return "v=0\r\n", nil }

func (mountMirrorStream) Stats() media.StreamStats { return media.StreamStats{} }

func (mountMirrorStream) Close() error { return nil }

// mountMirrors is the stream transport the route needs.
type mountMirrors struct{ stream mountMirrorStream }

func (m mountMirrors) Open(context.Context, string, string, media.MirrorTransportKind) (transportconnect.DeviceMirrorStream, error) {
	return m.stream, nil
}

func (m mountMirrors) Stream(string) (transportconnect.DeviceMirrorStream, bool) {
	return m.stream, true
}

// The compiler is what proves the transport satisfies the port the route serves:
// if this stops compiling, the route has no production mount again.
var _ transportconnect.DeviceMirrors = mountMirrors{}

// TestAConstructedLiveMirrorMountsTheRoute is the positive half of the gate.
func TestAConstructedLiveMirrorMountsTheRoute(t *testing.T) {
	route := service.DeviceMirrorRoute(mountMirrors{stream: mountMirrorStream{key: "drift-device-alpha-2abc1234"}}, mountSerials{}, "lab-token-value")
	if route.Path == "" {
		t.Fatal("a constructed stream transport mounted no route: the live mirror is unreachable even though the whole path exists")
	}
	if route.Handler == nil {
		t.Fatalf("route %q carries no handler", route.Path)
	}
}

// TestAnAbsentLiveMirrorMountsNothing: without a transport or a resolver there is
// no route, not a route that answers every request with a refusal.
func TestAnAbsentLiveMirrorMountsNothing(t *testing.T) {
	cases := []struct {
		name    string
		streams transportconnect.DeviceMirrors
		serials transportconnect.DeviceSerialResolver
	}{
		{name: "no stream transport", streams: nil, serials: mountSerials{}},
		{name: "no device resolver", streams: mountMirrors{}, serials: nil},
		{name: "nothing at all", streams: nil, serials: nil},
		{name: "a typed-nil stream transport", streams: typedNilMirrors(), serials: mountSerials{}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			route := service.DeviceMirrorRoute(test.streams, test.serials, "lab-token-value")
			if route.Path != "" || route.Handler != nil {
				t.Fatalf("an incomplete live mirror mounted %q: it advertises a surface that cannot work", route.Path)
			}
		})
	}
}

// TestAConstructedLiveMirrorMountsTheStreamEndpoint: the TCP transport is fetched
// from the service's own stream endpoint, so the endpoint is mounted exactly when
// the live mirror's transport was constructed - and not otherwise. A transport
// whose endpoint is missing is a transport that cannot work, which is the outcome
// this gate exists to prevent.
func TestAConstructedLiveMirrorMountsTheStreamEndpoint(t *testing.T) {
	route := service.MirrorStreamRoute(mountMirrors{stream: mountMirrorStream{key: "drift-device-alpha-2abc1234"}}, "lab-token-value")
	if route.Path == "" || route.Handler == nil {
		t.Fatal("a constructed stream transport mounted no stream endpoint: the TCP transport has nowhere to be fetched from")
	}
	if route.Path != transportconnect.MirrorStreamPath {
		t.Fatalf("the stream endpoint was mounted at %q, want %q", route.Path, transportconnect.MirrorStreamPath)
	}
}

// TestAnAbsentLiveMirrorMountsNoStreamEndpoint: without a stream transport there is
// no endpoint, not an endpoint whose every fetch can only be refused.
func TestAnAbsentLiveMirrorMountsNoStreamEndpoint(t *testing.T) {
	cases := []struct {
		name    string
		streams transportconnect.DeviceMirrors
	}{
		{name: "no stream transport", streams: nil},
		{name: "a typed-nil stream transport", streams: typedNilMirrors()},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			route := service.MirrorStreamRoute(test.streams, "lab-token-value")
			if route.Path != "" || route.Handler != nil {
				t.Fatalf("an absent live mirror mounted a stream endpoint at %q", route.Path)
			}
		})
	}
}

// typedNilMirrors is the shape a caller creates by passing an uninitialised
// pointer: a non-nil interface holding a nil pointer, which a plain nil check
// misses.
func typedNilMirrors() transportconnect.DeviceMirrors {
	var mirrors *mountMirrorPointer
	return mirrors
}

type mountMirrorPointer struct{ mountMirrors }

func (m *mountMirrorPointer) Open(context.Context, string, string, media.MirrorTransportKind) (transportconnect.DeviceMirrorStream, error) {
	return m.mountMirrors.stream, nil
}

func (m *mountMirrorPointer) Stream(string) (transportconnect.DeviceMirrorStream, bool) {
	return m.mountMirrors.stream, true
}
