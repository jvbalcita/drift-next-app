package connection

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"drift.local/drift-next/internal/edge/adb"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// CurrentEndpointSource reports the endpoints this plane currently observes, as
// the canonical `IPv4:port` a connect names.
//
// It exists because an address is not a fact this adapter is entitled to dial on
// its own: a reconnect that walks a list of addresses which were once configured -
// a retired range, a lease that moved, a handset that now answers somewhere else -
// spends the host's transport budget on units that are not there, and the only
// evidence it leaves is a line in the adb server's own log (ARC-231). The registry
// is where "where this device is" is recorded, so the registry is what bounds the
// dial.
//
// A nil source is NOT "no bound": it is a deployment that cannot read the registry,
// and every connect is refused rather than admitted, exactly as an absent port
// activation refuses every off-port endpoint. Fail closed, because the failure this
// prevents is a dial to an address nothing observes.
type CurrentEndpointSource interface {
	CurrentEndpoints(ctx context.Context) ([]string, error)
}

// CurrentEndpointSourceFunc adapts a function to the source, for a composition
// that wires one inline.
type CurrentEndpointSourceFunc func(ctx context.Context) ([]string, error)

func (f CurrentEndpointSourceFunc) CurrentEndpoints(ctx context.Context) ([]string, error) {
	return f(ctx)
}

// EndpointNotCurrentError is the refusal a caller reads when the address it named
// is not one this plane currently observes. It names the address and how many
// addresses ARE current, so an operator can tell "this device moved" from "this
// plane has observed nothing at all" - which are the same sentence otherwise.
type EndpointNotCurrentError struct {
	Endpoint string
	Current  int
}

func (e *EndpointNotCurrentError) Error() string {
	if e == nil {
		return "that endpoint is not one this plane currently observes"
	}
	if e.Current == 0 {
		return fmt.Sprintf("%s is not an endpoint this plane currently observes: it holds no current endpoint at all, so nothing has been observed where that address is", e.Endpoint)
	}
	return fmt.Sprintf("%s is not an endpoint this plane currently observes: it holds %d current endpoint(s), and that address is not one of them", e.Endpoint, e.Current)
}

// Code reports the stable refusal code this failure maps to: the address is not a
// fact the caller may act on, which is a precondition rather than invalid input.
func (e *EndpointNotCurrentError) Code() platformerrors.Code {
	return platformerrors.CodePreconditionFailed
}

// currentEndpointSet reads the source once and returns the addresses it holds,
// canonicalised and de-duplicated. A source that answered with nothing is an
// empty set rather than an error: "this plane currently observes no endpoint" is
// a reading, and every connect is refused against it.
func currentEndpointSet(ctx context.Context, source CurrentEndpointSource) (map[string]struct{}, error) {
	if source == nil {
		return nil, platformerrors.New(platformerrors.CodeUnavailable,
			"this deployment cannot read the endpoints it currently observes, so no connect can be bounded by one: wire the registry's current endpoints before connecting")
	}
	addresses, err := source.CurrentEndpoints(ctx)
	if err != nil {
		return nil, fmt.Errorf("the endpoints this plane currently observes could not be read, so no connect can be bounded by one: %w", err)
	}
	set := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		canonical, ok := CanonicalEndpoint(address)
		if !ok {
			// An address this adapter's own rule cannot spell is not one it could
			// dial either, so it is not admitted into the bound.
			continue
		}
		set[canonical] = struct{}{}
	}
	return set, nil
}

// CanonicalEndpoint spells a host and port the way a connect names it, and reports
// whether it is an address this adapter could dial at all. It is the one spelling
// both sides of the bound are compared in, so a registry row and a caller's
// argument cannot disagree about the same transport by whitespace or a leading
// zero.
func CanonicalEndpoint(address string) (string, bool) {
	trimmed := strings.TrimSpace(address)
	if adb.ValidateEndpoint(trimmed) != nil {
		return "", false
	}
	host, port, _ := strings.Cut(trimmed, ":")
	value, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return "", false
	}
	return host + ":" + strconv.FormatUint(value, 10), true
}

// CanonicalEndpoints spells a list of addresses, sorted, so a refusal can name
// what IS current in a stable order.
func CanonicalEndpoints(addresses []string) []string {
	out := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if canonical, ok := CanonicalEndpoint(address); ok {
			out = append(out, canonical)
		}
	}
	sort.Strings(out)
	return out
}
