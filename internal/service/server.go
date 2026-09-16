package service

import (
	"context"
	"errors"
	"net/http"
	"time"

	"drift.local/drift-next/gen/go/drift/v1/driftv1connect"
	"drift.local/drift-next/internal/edge/execution"
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

// DeviceInputRoute mounts the device input surface, and only when a dispatcher
// was actually constructed.
//
// A nil dispatcher yields an empty route, so a deployment that has not wired one
// exposes no device input surface at all — rather than a surface that can only
// answer with a refusal, which an operator surface would render as a control and
// then find dead. The gate is on the handler the constructor actually returned,
// not on the caller's argument, so a typed-nil dispatcher cannot slip past it.
//
// The route carries the same constant-time token check as the other local
// surfaces: loopback reachability alone is not authority for a hostile local
// caller.
func DeviceInputRoute(inputs transportconnect.DeviceInputs, token string) Route {
	handler := transportconnect.NewDeviceInputHandler(inputs)
	if handler == nil {
		return Route{}
	}
	path, connectHandler := driftv1connect.NewDeviceInputServiceHandler(handler)
	return Route{Path: path, Handler: RequireLabToken(token, connectHandler)}
}

// TextReferenceRoute mounts the local surface that registers a typed-text value
// with the reference registry, and only when a registry was actually
// constructed: a nil registry yields an empty route, so a deployment without one
// exposes no surface rather than a control that can only refuse.
//
// The route carries the same constant-time token check as the other local
// surfaces. This is the one boundary that admits operator-supplied content into
// the process, and loopback reachability alone is not authority for a hostile
// local caller.
func TextReferenceRoute(registry *execution.TextReferenceRegistry, token string) Route {
	handler := NewTextReferenceHandler(registry)
	if handler == nil {
		return Route{}
	}
	return Route{Path: TextReferencePath, Handler: RequireLabToken(token, handler)}
}

// ProductRoutes mounts the local product Connect surfaces when handlers were
// constructed against an open SQLite store. Empty handlers are skipped.
func ProductRoutes(handlers *transportconnect.ProductHandlers, token string) []Route {
	if handlers == nil {
		return nil
	}
	mount := func(path string, handler http.Handler) Route {
		if path == "" || handler == nil {
			return Route{}
		}
		return Route{Path: path, Handler: RequireLabToken(token, handler)}
	}
	return []Route{
		mount(driftv1connect.NewDeviceServiceHandler(handlers.Device)),
		mount(driftv1connect.NewNetworkProfileServiceHandler(handlers.NetworkProfile)),
		mount(driftv1connect.NewDiscoveryServiceHandler(handlers.Discovery)),
		mount(driftv1connect.NewGroupServiceHandler(handlers.Group)),
		mount(driftv1connect.NewEndpointServiceHandler(handlers.Endpoint)),
		mount(driftv1connect.NewObservationServiceHandler(handlers.Observation)),
		mount(driftv1connect.NewEventServiceHandler(handlers.Event)),
		mount(driftv1connect.NewEdgeAgentServiceHandler(handlers.EdgeAgent)),
		mount(driftv1connect.NewLeaseServiceHandler(handlers.Lease)),
		mount(driftv1connect.NewActionServiceHandler(handlers.Action)),
		mount(driftv1connect.NewAutomationAgentServiceHandler(handlers.AutomationAgent)),
		mount(driftv1connect.NewAccountServiceHandler(handlers.Account)),
		mount(driftv1connect.NewSettingsServiceHandler(handlers.Settings)),
		mount(driftv1connect.NewPolicyServiceHandler(handlers.Policy)),
		mount(driftv1connect.NewWorkflowServiceHandler(handlers.Workflow)),
		mount(driftv1connect.NewRunServiceHandler(handlers.Run)),
		mount(driftv1connect.NewRecordingServiceHandler(handlers.Recording)),
		mount(driftv1connect.NewSkillServiceHandler(handlers.Skill)),
		mount(driftv1connect.NewWorkspaceServiceHandler(handlers.Workspace)),
		mount(driftv1connect.NewMirrorServiceHandler(handlers.Mirror)),
		mount(driftv1connect.NewRuntimeServiceHandler(handlers.Runtime)),
	}
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
		Handler:           localCORS(mux),
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
