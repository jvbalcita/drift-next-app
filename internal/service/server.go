package service

import (
	"context"
	"errors"
	"net/http"
	"time"

	"drift.local/drift-next/gen/go/drift/v1/driftv1connect"
	"drift.local/drift-next/internal/health"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// Route is one mounted Connect service: the generated path prefix and handler.
type Route struct {
	Path    string
	Handler http.Handler
}

// LabAdapterRoute builds the Connect route for the lab adapter boundary. The
// route only exists when a lab service is constructed, so a deployment without
// one exposes no lab surface at all.
//
// A non-empty token guards every lab RPC with a constant-time header check, so
// loopback reachability alone is not authority for a hostile local caller.
func LabAdapterRoute(service transportconnect.LabAdapter, token string) Route {
	path, handler := driftv1connect.NewLabAdapterServiceHandler(
		transportconnect.NewLabAdapterHandler(service),
	)
	return Route{Path: path, Handler: RequireLabToken(token, handler)}
}

// LabRegistrationRoute builds the Connect route for controlled one-device
// registration. The route is only mounted when a registration service is
// constructed; prerequisite booleans never come from the client.
func LabRegistrationRoute(service transportconnect.LabRegistration, token string, allowedPorts []uint16) Route {
	path, handler := driftv1connect.NewLabRegistrationServiceHandler(
		transportconnect.NewLabRegistrationHandler(service, allowedPorts),
	)
	return Route{Path: path, Handler: RequireLabToken(token, handler)}
}

// LabRegistrationRouteWithStore is LabRegistrationRoute with durable SQLite
// staging for Approve→Register. Pass a nil store for in-memory-only operation.
func LabRegistrationRouteWithStore(service transportconnect.LabRegistration, store transportconnect.LabRegistrationStore, token string, allowedPorts []uint16) Route {
	path, handler := driftv1connect.NewLabRegistrationServiceHandler(
		transportconnect.NewLabRegistrationHandlerWithStore(service, store, allowedPorts),
	)
	return Route{Path: path, Handler: RequireLabToken(token, handler)}
}

// ArtifactRoute mounts artifact list/get/read/delete/health RPCs only when an
// artifact application service has been constructed.
func ArtifactRoute(service transportconnect.ArtifactAPI, token string) Route {
	if service == nil {
		return Route{}
	}
	path, handler := driftv1connect.NewArtifactServiceHandler(
		transportconnect.NewArtifactHandler(service),
	)
	return Route{Path: path, Handler: RequireLabToken(token, handler)}
}

// NewHTTPServer returns a loopback-oriented server exposing health endpoints
// plus any explicitly mounted Connect routes. Adding a route is deliberate:
// nothing is mounted by reflection or by package initialization.
func NewHTTPServer(name, address string, routes ...Route) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/", health.Handler(name, func() bool { return true }))
	for _, route := range routes {
		if route.Path == "" || route.Handler == nil {
			continue
		}
		mux.Handle(route.Path, route.Handler)
	}
	return &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func Serve(ctx context.Context, server *http.Server) error {
	errorsCh := make(chan error, 1)
	go func() {
		errorsCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errorsCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return err
		}
		return nil
	}
}
