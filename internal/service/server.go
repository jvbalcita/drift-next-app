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

// DeviceSettingsRoute mounts the fleet device-settings surface, and only when an
// applier was actually constructed.
//
// A nil applier yields an empty route, so a deployment that has not wired one
// exposes no settings surface at all — rather than a surface that can only
// answer with a refusal, which an operator surface would render as a control and
// then find dead. The gate is on the handler the constructor actually returned,
// not on the caller's argument, so a typed-nil applier cannot slip past it.
//
// The route carries the same constant-time token check as the other local
// surfaces. It reaches devices and changes their settings, so loopback
// reachability alone is not authority for a hostile local caller.
func DeviceSettingsRoute(settings transportconnect.DeviceSettings, token string) Route {
	handler := transportconnect.NewDeviceSettingsHandler(settings)
	if handler == nil {
		return Route{}
	}
	path, connectHandler := driftv1connect.NewDeviceSettingsServiceHandler(handler)
	return Route{Path: path, Handler: RequireLabToken(token, connectHandler)}
}

// DeviceOperationsRoute mounts the big-frame control panel's device-operation
// surface, and only when an applier was actually constructed.
//
// A nil applier yields an empty route, so a deployment that has not wired one
// exposes no operation surface at all — rather than a surface that can only
// answer with a refusal, which an operator surface would render as a control and
// then find dead. The gate is on the handler the constructor actually returned,
// not on the caller's argument, so a typed-nil applier cannot slip past it.
//
// The route carries the same constant-time token check as the other local
// surfaces. It reaches devices, restarts them and moves bytes onto them, so
// loopback reachability alone is not authority for a hostile local caller.
func DeviceOperationsRoute(operations transportconnect.DeviceOperations, token string) Route {
	handler := transportconnect.NewDeviceOperationsHandler(operations)
	if handler == nil {
		return Route{}
	}
	path, connectHandler := driftv1connect.NewDeviceOperationsServiceHandler(handler)
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

// ConnectionRoute mounts the transport surface the OTG Setup tab performs:
// connect, change transport mode, activate port, and restart the adb server.
//
// It is a route only when a transport boundary was constructed. A nil boundary
// yields an empty route, so a deployment without a connector, restarter or
// activator exposes no transport control at all rather than a control that can
// only refuse — the same rule the device input and text reference routes
// already apply.
//
// The route carries the same constant-time token check as the other local
// surfaces. It reaches devices, so loopback reachability alone is not authority
// for a hostile local caller.
func ConnectionRoute(operations transportconnect.Connections, token string) Route {
	handler := transportconnect.NewConnectionHandler(operations)
	if handler == nil {
		return Route{}
	}
	path, connectHandler := driftv1connect.NewConnectionServiceHandler(handler)
	return Route{Path: path, Handler: RequireLabToken(token, connectHandler)}
}

// DeviceMirrorRoute mounts the live mirror surface, and only when a stream
// transport and a device-to-serial resolver were both constructed.
//
// A nil transport, a nil resolver or a typed-nil one yields an empty route, so a
// deployment without a live mirror exposes no stream surface at all — rather than
// a surface that can only answer with a refusal, which an operator surface would
// render as a control and then find dead. The gate is on the handler the
// constructor actually returned, not on the caller's argument.
//
// capacity is the engine whose bound this surface publishes, and refusals is where
// a refused stream is written down. Neither gates the mount: a route that exists at
// all was built over an engine, so it has a bound to state, and a deployment whose
// store is open has somewhere to record a refusal. A surface mounted without them
// would still carry streams — it just could not say what its own capacity is, and
// would leave a refusal recorded nowhere.
//
// The route carries the same constant-time token check as the other local
// surfaces. It reaches devices and carries their screens, so loopback
// reachability alone is not authority for a hostile local caller.
func DeviceMirrorRoute(streams transportconnect.DeviceMirrors, serials transportconnect.DeviceSerialResolver, capacity transportconnect.MirrorCapacitySource, refusals transportconnect.MirrorRefusalRecorder, token string) Route {
	handler := transportconnect.NewDeviceMirrorHandler(streams, serials, capacity, refusals)
	if handler == nil {
		return Route{}
	}
	path, connectHandler := driftv1connect.NewDeviceMirrorServiceHandler(handler)
	return Route{Path: path, Handler: RequireLabToken(token, connectHandler)}
}

// GridPreviewRoute mounts the fleet grid's still previews, and only when a capture
// set and a device-to-serial resolver were both constructed.
//
// A nil capture set and a typed-nil one both yield an empty route, so a deployment
// with no frame engine exposes no grid surface at all - rather than a surface that
// can only answer with a refusal, which a console renders as a grid of dead tiles.
// The gate is on the handler the constructor actually returned, not on the caller's
// argument.
//
// The route carries the same constant-time token check as the other local
// surfaces. It reaches devices and carries their screens, so loopback reachability
// alone is not authority for a hostile local caller.
func GridPreviewRoute(grid transportconnect.GridStills, serials transportconnect.DeviceSerialResolver, token string) Route {
	handler := transportconnect.NewGridPreviewHandler(grid, serials)
	if handler == nil {
		return Route{}
	}
	path, connectHandler := driftv1connect.NewGridPreviewServiceHandler(handler)
	return Route{Path: path, Handler: RequireLabToken(token, connectHandler)}
}

// MirrorStreamRoute mounts the per-device stream endpoint a browser fetches when
// the operator's transport is TCP: the response body is one device's live mirror
// as fragmented MP4.
//
// It carries the same constant-time token check as every other local surface, and
// it is mounted only when the live mirror's transport was constructed - a mount
// gate on the handler the constructor actually returned, not on the caller's
// argument. A deployment without a live mirror therefore exposes no stream
// endpoint at all, rather than one whose every request can only be refused.
//
// The endpoint is served on the same listener as everything else, which startup
// has already required to be loopback (AGENTS.md section 9): broadening the
// service's exposure is a deployment decision, and it is not made here.
func MirrorStreamRoute(streams transportconnect.DeviceMirrors, token string) Route {
	handler := transportconnect.NewMirrorStreamHTTPHandler(streams)
	if handler == nil {
		return Route{}
	}
	return Route{Path: transportconnect.MirrorStreamPath, Handler: RequireLabToken(token, handler)}
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
