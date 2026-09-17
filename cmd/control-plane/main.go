package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	dbmigrations "drift.local/drift-next/db/migrations"
	"drift.local/drift-next/internal/artifacts"
	"drift.local/drift-next/internal/artifacts/cas"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/edge/connection"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/ids"
	migrationrunner "drift.local/drift-next/internal/platform/migrations"
	"drift.local/drift-next/internal/product"
	"drift.local/drift-next/internal/service"
	store "drift.local/drift-next/internal/store/sqlite"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

const (
	envControlPlaneDB  = "DRIFT_CONTROL_PLANE_DB"
	envArtifactCASRoot = "DRIFT_ARTIFACT_CAS_ROOT"
)

func main() {
	// The listener stays on loopback: this is a local operator service. An
	// operator-supplied address is validated rather than trusted, and a
	// non-loopback address fails closed instead of quietly exposing the lab
	// surface to the network.
	address := os.Getenv("DRIFT_CONTROL_PLANE_ADDR")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	if err := service.ValidateLoopbackAddress(address); err != nil {
		log.Fatalf("refusing to start: %v (set DRIFT_CONTROL_PLANE_ADDR to 127.0.0.1:PORT, [::1]:PORT, or localhost:PORT)", err)
	}

	labToken := strings.TrimSpace(os.Getenv(lab.EnvRuntimeToken))
	if labToken == "" {
		labToken = strings.TrimSpace(os.Getenv(lab.EnvLabToken))
	}
	if lab.LabModeRequested(os.LookupEnv) && labToken == "" {
		// Loopback keeps the surface off the network, but it does not
		// distinguish a hostile local process from the console. Real-device
		// mode therefore requires a shared secret as well.
		log.Fatalf("refusing to start lab mode: %s must be set to a non-empty local lab token", lab.EnvLabToken)
	}

	// Keep the interface typed as ArtifactAPI so a nil *store.DB-derived value
	// is not stored as a non-nil interface value.
	var artifactAPI transportconnect.ArtifactAPI
	var productHandlers *transportconnect.ProductHandlers
	var labOpts []lab.Option
	dbPath := strings.TrimSpace(os.Getenv(envControlPlaneDB))
	if dbPath == "" {
		dbPath = product.DefaultControlPlaneDBPath()
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(dbPath), 0o700); mkdirErr != nil {
		log.Fatalf("refusing to start: create control-plane data directory: %v", mkdirErr)
	}
	// drift and the control plane share this database and can run at different
	// revisions, so a stale binary must refuse to serve a schema that a newer
	// build already migrated. This runs before any service is constructed.
	if guardErr := migrationrunner.CheckLedgerNotAhead(context.Background(), dbPath, dbmigrations.SQLiteFiles); guardErr != nil {
		log.Fatalf("refusing to start: %v", guardErr)
	}
	db, openErr := store.Open(context.Background(), dbPath, store.Options{})
	if openErr != nil {
		log.Fatalf("refusing to start: open control-plane sqlite: %v", openErr)
	}
	defer func() { _ = db.Close() }()
	workspaceID := organizations.WorkspaceID("workspace-lab-local")
	if createErr := store.NewWorkspaceService(db).Create(context.Background(), organizations.Workspace{
		ID: workspaceID, Name: "Local Workspace", State: organizations.WorkspaceActive,
	}, "system", "control-plane"); createErr != nil && platformerrors.CodeOf(createErr) != platformerrors.CodeConflict {
		log.Fatalf("refusing to start: ensure lab workspace: %v", createErr)
	}
	log.Printf("control-plane durable store enabled at %s", dbPath)

	casRoot := strings.TrimSpace(os.Getenv(envArtifactCASRoot))
	if casRoot == "" {
		casRoot = filepath.Join(filepath.Dir(dbPath), "artifacts", "cas")
	}
	if !filepath.IsAbs(casRoot) {
		absRoot, absErr := filepath.Abs(casRoot)
		if absErr != nil {
			log.Fatalf("refusing to start: resolve %s: %v", envArtifactCASRoot, absErr)
		}
		casRoot = absRoot
	}
	casStore, casErr := cas.Open(casRoot)
	if casErr != nil {
		log.Fatalf("refusing to start: open artifact CAS at %s: %v", casRoot, casErr)
	}
	artifactService, artifactErr := artifacts.NewService(store.NewArtifactService(db), casStore)
	if artifactErr != nil {
		log.Fatalf("refusing to start: construct artifact service: %v", artifactErr)
	}
	artifactAPI = artifactService
	labOpts = append(labOpts, lab.WithEvidencePersister(artifacts.CapturePersister{
		Service:   artifactService,
		Workspace: workspaceID,
	}))
	log.Printf("control-plane local artifact CAS enabled at %s", casStore.Root())

	// Lab mode requires an explicit opt-in plus a configured adb path; anything
	// else yields a deterministic mock service that never reaches a device.
	labService, err := lab.NewServiceFromEnv(os.LookupEnv, labOpts...)
	if err != nil {
		log.Fatal(err)
	}
	labMode := "mock"
	if lab.LabModeRequested(os.LookupEnv) {
		labMode = "lab"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// One adapter enumeration over the lab runtime, read by both the startup
	// scan and the post-launch watcher below: a device that attaches after
	// launch is seen through the same view the scan observes, not a second one.
	enumerator := product.NewLabRuntimeEnumerator(labService)
	scanner := discovery.NewAuthorizedLabScanner(discovery.LabScannerConfig{
		Authorized: true,
		LabMode:    lab.LabModeRequested(os.LookupEnv),
		Enumerator: enumerator,
	})
	productHandlers = transportconnect.NewProductHandlers(db, scanner)
	actionRuntime := execution.NewRegistry(labService, db)
	productHandlers.Action.SetExecutor(actionRuntime)
	defer func() { _ = actionRuntime.Close() }()

	// One discovery service, and therefore one serial-keyed upsert: the startup
	// scan and the post-launch watcher below both record through this instance,
	// so a device that attaches after launch resolves to the identity it already
	// has instead of minting a second one (AGENTS.md section 2).
	discoveryService := discovery.NewService(db, scanner)

	// One bounded auto-scan of the default network profile per process start.
	// It reuses the manual scan machinery, is cancelled by the same shutdown
	// context as the listener, is owned by main (see the wait below), and never
	// aborts startup: every outcome is recorded and logged instead.
	autoScan := product.NewStartupAutoScanner(product.StartupAutoScanConfig{
		Workspaces: store.NewWorkspaceRepository(db),
		Profiles:   store.NewNetworkProfileRepository(db),
		Runner:     discoveryService,
		Recorder:   db,
		StartedAt:  time.Now().UTC(),
	})
	autoScanDone := make(chan struct{})
	go func() {
		defer close(autoScanDone)
		log.Printf("%s", autoScan.Run(ctx).Report())
	}()

	// Post-launch arrivals: the startup scan covers what is attached when the
	// process starts, and this covers what attaches after it. The watcher polls
	// the same adapter enumeration the scan reads, is cancelled by the same
	// shutdown context as the listener, is owned by main (see the wait below),
	// and never aborts startup: every arrival, departure, and failed poll is
	// reported as it happens rather than held back to shutdown.
	//
	// Every arrival it observes is recorded through the same serial-keyed upsert
	// the startup scan writes with, on the poll's own bounded context, so the
	// device is in the registry the console reads by the time the next poll runs -
	// no operator action, no scan. The watcher still owns no scan, no device
	// execution, and no worker of its own: recording an arrival is that one
	// bounded write, and it happens on the poll that observed it.
	transportWatch := product.NewTransportWatcher(product.TransportWatcherConfig{
		Enumerator: enumerator,
		Sink: func(ctx context.Context, arrivals []discovery.ObservedDevice) error {
			_, recordErr := discoveryService.RecordArrivals(ctx, workspaceID, arrivals, product.WatcherActorType, product.WatcherActorID)
			return recordErr
		},
		// A departure is recorded the same way an arrival is: on the poll that
		// observed it, on that poll's bounded context. Without this the watcher
		// counts a departure and discards it, and a device unplugged an hour ago
		// still answers as present to every reader of the registry.
		DepartureSink: func(ctx context.Context, departures []discovery.ObservedDevice) error {
			return discoveryService.RecordDepartures(ctx, workspaceID, departures, product.WatcherActorType, product.WatcherActorID)
		},
	})
	transportWatchDone := make(chan struct{})
	go func() {
		defer close(transportWatchDone)
		log.Printf("%s", transportWatch.Run(ctx).Report())
	}()

	routes := []service.Route{
		service.LabAdapterRoute(labService, labToken),
	}
	if artifactAPI != nil {
		routes = append(routes, service.ArtifactRoute(artifactAPI, labToken))
	}
	routes = append(routes, service.ProductRoutes(productHandlers, labToken)...)
	// The text reference registry is the boundary that owns a typed-text value
	// until it is released at dispatch, and the registration surface below is
	// where such a value enters the process. The registry is constructed before
	// the input surface because the dispatcher is given it as the typed-text
	// resolver: typed text is only dispatchable if something can register a
	// reference for it to release (ARC-107).
	textReferences, referenceErr := execution.NewTextReferenceRegistry(clock.System{}, execution.DefaultTextReferenceTTL, execution.DefaultTextReferenceCapacity)
	if referenceErr != nil {
		log.Printf("text reference surface not mounted: %v", referenceErr)
	}

	// The typed device input surface. It is mounted only when the whole path was
	// constructed: an empty Route mounts nothing, so a missing dependency degrades
	// to "no input surface" rather than to a route that cannot dispatch anything
	// (AGENTS.md section 6).
	inputMounted := false
	if inputRoute := deviceInputRoute(labService, actionRuntime, textReferences, db, labToken); inputRoute.Path != "" {
		routes = append(routes, inputRoute)
		inputMounted = true
	}
	// The one boundary that admits operator-supplied content into this process,
	// mounted only when a registry was constructed to hold what it admits.
	referenceMounted := false
	if referenceRoute := service.TextReferenceRoute(textReferences, labToken); referenceRoute.Path != "" {
		routes = append(routes, referenceRoute)
		referenceMounted = true
	}
	// The transport surface the OTG Setup tab performs: connect, change a
	// device's transport mode, activate an observed port, restart the adb server.
	// It is mounted only when the whole path was constructed. An empty Route
	// mounts nothing, and a route whose connector cannot reach the adb server is
	// worse than no route at all, because it advertises a surface that cannot
	// work (AGENTS.md section 6). Each reason is logged rather than swallowed, so
	// a deployment that expected the surface can see which dependency was missing.
	connectionMounted := false
	acceptedPorts, acceptedErr := acceptedProfilePorts(context.Background(), store.NewNetworkProfileRepository(db), workspaceID)
	if acceptedErr != nil {
		log.Printf("transport surface not mounted: %v", acceptedErr)
	} else if operations, operationsErr := connectionOperations(labService, acceptedPorts); operationsErr != nil {
		log.Printf("transport surface not mounted: %v", operationsErr)
	} else if connectionRoute := service.ConnectionRoute(operations, labToken); connectionRoute.Path != "" {
		routes = append(routes, connectionRoute)
		connectionMounted = true
	}
	server := service.NewHTTPServer("control-plane", address, routes...)
	log.Printf("control-plane listening on %s with lab adapter in %s mode (lab token %s, device input surface %s, text reference surface %s, transport surface %s)", address, labMode, tokenState(labToken), mountState(inputMounted), referenceMountState(referenceMounted), connectionMountState(connectionMounted))
	serveErr := service.Serve(ctx, server)
	// The startup scan and the post-launch watcher are owned work, not detached
	// workers: wait for both to obey cancellation before the process returns.
	<-autoScanDone
	<-transportWatchDone
	if serveErr != nil {
		log.Fatal(serveErr)
	}
}

// tokenState reports whether the lab route is guarded without ever rendering
// the token itself.
func tokenState(token string) string {
	if token == "" {
		return "not configured; loopback-only"
	}
	return "required"
}

// mountState reports whether the device input surface was mounted. It does not
// imply that a mounted surface will accept an input: the kernel still decides
// every dispatch.
func mountState(mounted bool) string {
	if mounted {
		return "mounted"
	}
	return "not mounted; the device input path was not constructed"
}

// referenceMountState reports whether the registration surface was mounted, and
// separately from the input surface, because the two fail for different reasons:
// a registry that could not be constructed is not a dispatcher that was not.
func referenceMountState(mounted bool) string {
	if mounted {
		return "mounted"
	}
	return "not mounted; no reference registry was constructed"
}

// inputObservationOperator is the identity the composition reads a device's
// postcondition observation under. It is the composition's own identity rather
// than a user's: the operator who asked for the action is the actor the kernel
// records on the attempt, and this read happens on that action's behalf. Naming a
// service identity keeps the read attributable without inventing a person.
const inputObservationOperator = "control-plane-input-observer"

// observationSource builds the postcondition observer's per-device observation
// read over the lab boundary's read-only observation adapter. The adapter names
// the serial on every capture, so the read is authorized against the attached
// transports rather than against session state.
func observationSource(labService *lab.Service) execution.ObservationSourceFactory {
	return func(serial string) (execution.ObservationSource, error) {
		return lab.NewObservationAdapter(labService, serial, inputObservationOperator)
	}
}

// deviceInputRoute builds the typed device input route, or an empty Route when the
// path cannot be constructed.
//
// Every dependency is checked, and every absence returns an empty Route, which
// mounts nothing at all. That is deliberate rather than defensive: a route whose
// dispatcher cannot dispatch is worse than no route, because it advertises a
// surface that cannot work (AGENTS.md section 6). Each reason is logged rather
// than swallowed, so a deployment that expected the surface can see which
// dependency was missing instead of finding no route and no explanation.
//
// The composed path, in the order a request travels it: the transport is the
// device's own allow-listed runner (4a); the readiness probe judges the device from
// the lab boundary's attached set (4b); the postcondition observer reads the
// device's current observation (slice 3); the dispatcher carries the whole P7
// contract with the evidence recorder bound; and the application boundary resolves
// the device to its serial and assigns the attempt identity (4c), which is what
// satisfies the port this route serves.
func deviceInputRoute(labService *lab.Service, resolver *execution.Registry, textReferences *execution.TextReferenceRegistry, db *store.DB, token string) service.Route {
	transport, err := execution.NewInputTransportFromAllowlisted(labService.DeviceTransport())
	if err != nil {
		log.Printf("device input surface not mounted: %v", err)
		return service.Route{}
	}
	observer, err := execution.NewObservationPostconditionObserver(resolver, observationSource(labService))
	if err != nil {
		log.Printf("device input surface not mounted: %v", err)
		return service.Route{}
	}
	deviceState, err := execution.NewTransportObserverFromAttached(labService)
	if err != nil {
		log.Printf("device input surface not mounted: %v", err)
		return service.Route{}
	}
	dispatcher, err := execution.NewInputDispatcher(
		store.NewActionService(db),
		execution.NewStoreControlProbe(db, deviceState),
		observer,
		transport,
		// The typed-text resolver: the same registry the registration surface
		// writes to, so a typed-text payload releases a value only if one was
		// registered for its workspace. With no registry (no surface was
		// constructed) the primitive still fails closed on its own.
		textReferences,
		execution.WithEvidenceRecorder(store.NewActionEvidenceService(db)),
	)
	if err != nil {
		log.Printf("device input surface not mounted: %v", err)
		return service.Route{}
	}
	boundary, err := transportconnect.NewDeviceInputBoundary(dispatcher, resolver, ids.NewRandom())
	if err != nil {
		log.Printf("device input surface not mounted: %v", err)
		return service.Route{}
	}
	return service.DeviceInputRoute(boundary, token)
}

// acceptedProfilePorts reads the workspace's DEFAULT network profile's declared
// ports: the set a transport may be opened on without an activation. The profile
// is the operator's own declaration, validated when it was written, so the policy
// comes from what the operator said rather than from a constant in this binary,
// and it is the same declaration the scan is bounded by.
//
// A workspace with no default profile yields an error and the transport surface is
// not mounted, because a policy with no accepted set refuses every endpoint and
// would advertise a surface that can only refuse (AGENTS.md section 7).
func acceptedProfilePorts(ctx context.Context, profiles *store.NetworkProfileRepository, workspace organizations.WorkspaceID) ([]uint16, error) {
	listed, err := profiles.List(ctx, workspace)
	if err != nil {
		return nil, err
	}
	for _, profile := range listed {
		if profile.IsDefault {
			return profile.Ports, nil
		}
	}
	return nil, platformerrors.New(platformerrors.CodeInvalidInput, "no default network profile is configured, so no port is accepted for a transport")
}

// connectionOperations builds the transport boundary over the lab service's own
// runners. It is the composition root's job: the service exposes the host and
// device runners it was constructed with, and this assembles them into the single
// boundary the transport surface depends on. Nothing here is derived by type
// assertion on a stored field, because the service's own rule is that exposing a
// runner is an explicit opt-in and never an implicit one.
//
// The enumerator is the lab service itself: it already enumerates attached
// transports as adb-discovered devices, so a restart re-establishes endpoints from
// the same view the rest of the product sees rather than from a second one.
//
// The activator is given no Wait: NewActivator fills the production default, which
// bounds the settle after tcpip against the caller's context rather than sleeping
// through a cancellation. Only a test injects one.
func connectionOperations(labService *lab.Service, acceptedPorts []uint16) (transportconnect.ConnectionOperations, error) {
	host := labService.HostTransport()
	device := labService.DeviceTransport()
	enumerator := labService.Enumerator()
	if host == nil || device == nil || enumerator == nil {
		return transportconnect.ConnectionOperations{}, platformerrors.New(platformerrors.CodeUnavailable, "the lab service exposes no transport runner, so no transport operation can reach a device")
	}
	policy := connection.NewPortPolicy(acceptedPorts)
	// Activation is the operator's decision, recorded per device. It lives in
	// process memory because nothing durable is required for it yet: a restart
	// forgets it, which is fail-closed rather than permissive.
	activations := connection.NewPortActivations()
	connector, connectorErr := connection.NewConnector(connection.ConnectorConfig{Runner: host, Policy: policy, Activations: activations})
	if connectorErr != nil {
		return transportconnect.ConnectionOperations{}, connectorErr
	}
	restarter, restarterErr := connection.NewRestarter(connection.RestarterConfig{Runner: host, Enumerator: enumerator, Policy: policy})
	if restarterErr != nil {
		return transportconnect.ConnectionOperations{}, restarterErr
	}
	activator, activatorErr := connection.NewActivator(connection.ActivatorConfig{Runner: device, Enumerator: enumerator})
	if activatorErr != nil {
		return transportconnect.ConnectionOperations{}, activatorErr
	}
	return transportconnect.ConnectionOperations{Connector: connector, Restarter: restarter, Activator: activator}, nil
}

// connectionMountState reports whether the transport surface was mounted. Like the
// other state reporters it says nothing about whether a mounted surface will accept
// an action: the port policy and the kernel still decide every operation.
func connectionMountState(mounted bool) string {
	if mounted {
		return "mounted"
	}
	return "not mounted; the transport path was not constructed"
}
