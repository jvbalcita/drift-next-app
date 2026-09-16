package lab

import (
	"context"
	"errors"

	"drift.local/drift-next/internal/edge/adb"
)

// ErrTransportRequired reports a transport opt-in that binds nothing.
var ErrTransportRequired = errors.New("a lab device transport is required to expose one")

// DeviceTransport is the device's own allow-listed runner, exposed so the
// composition root can build the device input path over the same adapter this
// service already reaches devices through.
//
// It is deliberately one operation over an argument array this repository's
// builders produced: it is not a shell, and nothing in this package lets a
// caller author one. (*adb.Adapter).RunAllowlisted satisfies it.
//
// The value handed out is the adapter itself, never a wrapper around it. A
// counting, logging or instrumenting transport in this position makes a
// read-only precondition read — the render-size cross-check — look like an
// attempted input, and the device actor then turns the attempt indeterminate
// (card ARC-76). The accessor exists so the composition root passes the device's
// own transport rather than building a second one that behaves differently.
type DeviceTransport interface {
	RunAllowlisted(ctx context.Context, serial string, args []string) (adb.Result, error)
}

// WithLabTransport binds the device's own allow-listed runner for an input path
// the composition root builds.
//
// It is a separate decision from WithLabAdapters on purpose: reaching a device
// for read-only observation and handing that same adapter to the input path are
// different powers, and a service that was not asked for the second must not
// expose it. The option refuses a nil transport rather than binding one, so a
// missing transport is a construction failure instead of a nil that every later
// caller has to remember to check.
func WithLabTransport(transport DeviceTransport) Option {
	return func(s *Service) error {
		if transport == nil {
			return ErrTransportRequired
		}
		s.transport = transport
		return nil
	}
}

// DeviceTransport returns the device's own allow-listed runner this service was
// built with, or nil when it was built without one.
//
// Nil is not an error here: it means no transport was bound, and the caller must
// build no input path and mount no input route from this service. That is the
// composition rule this accessor serves — no constructed dispatcher, no route —
// and it is why the absence is visible rather than implicit.
func (s *Service) DeviceTransport() DeviceTransport {
	if s == nil {
		return nil
	}
	return s.transport
}
