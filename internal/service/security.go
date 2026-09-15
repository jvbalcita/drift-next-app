package service

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// LabTokenHeader carries the local lab shared secret. It authenticates a local
// caller to the lab adapter route; it is not a device credential, a lease, or a
// fencing token.
const LabTokenHeader = "X-Drift-Lab-Token"

// ErrAddressNotLoopback reports a listen address that would expose the local
// operator service beyond the host.
var ErrAddressNotLoopback = errors.New("control-plane listen address must be loopback")

// ValidateLoopbackAddress fails closed on any listen address that is not bound
// to loopback. An empty host (":8080") binds every interface, so it is refused
// even though it is a common Go default.
func ValidateLoopbackAddress(address string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return fmt.Errorf("%w: %q is not a host:port address", ErrAddressNotLoopback, address)
	}
	if strings.TrimSpace(port) == "" {
		return fmt.Errorf("%w: %q has no port", ErrAddressNotLoopback, address)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("%w: %q binds every interface; use 127.0.0.1 or [::1]", ErrAddressNotLoopback, address)
	}
	// "localhost" is accepted as a literal rather than resolved: resolution is
	// host configuration an attacker may influence.
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: %q is a hostname; use 127.0.0.1, ::1, or localhost", ErrAddressNotLoopback, address)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("%w: %q is reachable off-host", ErrAddressNotLoopback, address)
	}
	return nil
}

// RequireLabToken guards a handler with a constant-time shared-secret check.
// An empty token returns the handler unguarded: the caller decides whether an
// unguarded lab surface is acceptable, and main refuses that combination in
// lab mode.
func RequireLabToken(token string, next http.Handler) http.Handler {
	expected := []byte(strings.TrimSpace(token))
	if len(expected) == 0 {
		return next
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		presented := []byte(request.Header.Get(LabTokenHeader))
		if subtle.ConstantTimeCompare(presented, expected) != 1 {
			// The body stays generic: a local caller learns nothing about the
			// configured token, the lab mode, or the confirmed target.
			http.Error(writer, "lab adapter requires a valid local lab token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
